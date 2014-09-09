package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"source.uit.no/lars.tiede/incoming/upload"

	"github.com/gorilla/websocket"
)

func acceptAllOrigins(r *http.Request) bool {
	return true
}

var conn_upgrader = websocket.Upgrader{
	//ReadBufferSize:  32768,
	//WriteBufferSize: 32768,
	CheckOrigin: acceptAllOrigins,
}

/* upload request from the browser. This is the first message that the browser
sends to incoming!!, requesting to upload a file with a given upload id.
*/
type MsgUploadReq struct {
	Id          string
	LengthBytes int64
}

// MsgUploadConf is sent to the browser and contains parameters for the upload,
// such as chunk size per message, file position to resume from, and how
// many messages sends the sender may be ahead of receiving acks.
type MsgUploadConf struct {
	// size of a chunk (i.e., single message payload size), in kilobytes
	ChunkSizeKB uint

	// position in file to resume uploading from (for now, always 0)
	FilePos int64

	// how many sends may sender be ahead of receiving acks? If 1, sender will
	// send message (n+1) only after ack for message (n) has been received.
	SendAhead uint
}

type MsgAck struct {
	Ack bool
}

type MsgChunkAck struct {
	ChunkSize int64
}

type MsgError struct {
	ErrorCode int
	Msg       string
}

type MsgAllDone struct {
	Success bool // we need *some* field
}

// closeWebsocketNormally is a shortcut for sending a 'close' control message
// with 'normal closure' and timeout given in app config
func closeWebsocketNormally(conn *websocket.Conn, msg string) (err error) {
	err = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
		time.Now().Add(time.Duration(appVars.config.UploadMaxIdleDurationS)*time.Second))
	return
}

type wsReadResult struct {
	messageType int
	data        []byte
	err         error
}

type wsWriteCmd struct {
	messageType int
	data        []byte
	ch_ret      chan error
}

// wsConnHandler starts goroutines for forever reading from, and writing to, a
// websocket connection. Reads can be read from the ch_r channel, writes be
// sent to the ch_w channel.
//
// When a read from the websocket returns an error (which for example happens
// when the connection is closed), the read goroutine will terminate, but not
// right away. The faulty read is available on the read channel for a while
// before a timeout kicks in and the channel is closed. This is a little weird,
// but it ensures that faulty reads can be read by some listening goroutine,
// while it is at the same time guaranteed that the goroutine terminates even
// if there is no listener.
//
// When the caller closes the write channel (don't forget to do that!),
// wsConnHandler will close the websocket connection (with Close(), not with a
// control message) and terminate both goroutines. If you want to close the
// websocket with a control message, just do it by sending a control message
// directly over the conn object (this is legal).  After that, close the write
// channel.
func wsConnHandler(c *websocket.Conn) (<-chan *wsReadResult,
	chan<- *wsWriteCmd) {

	// channels we expose
	ch_r := make(chan *wsReadResult)
	ch_w := make(chan *wsWriteCmd)

	// reader
	go func() {
		for cont := true; cont; {
			// read from websocket forever
			res := new(wsReadResult)
			res.messageType, res.data, res.err = c.ReadMessage() // err on socket close

			if res.err == nil {
				// got a message? send to read channel and read from websocket again
				ch_r <- res
			} else {
				log.Printf("ws conn handler reader got error (normal at close)")
				// got an error from the read? offer on read channel until timeout.
				// Eventually, break out of loop
				select {
				case ch_r <- res:
					cont = false
				case <-time.After(30 * time.Second):
					cont = false
				}
			}
		}
		close(ch_r)
		log.Printf("ws conn handler reader terminates")
		return
	}()

	// writer
	go func() {
		// recv from ch_w and send what is received over WriteMessage until channel
		// is closed
		for cmd := range ch_w {
			err := c.WriteMessage(cmd.messageType, cmd.data)
			cmd.ch_ret <- err
		}
		// when channel is closed, close the websocket
		log.Printf("ws conn handler writer closes websocket connection and terminates")
		c.Close() // reader goroutine will get an error from ReadMessage()
		return
	}()
	return ch_r, ch_w
}

func websocketHandler(w http.ResponseWriter, r *http.Request) {
	// "upgrade" connection to websocket connection
	conn, err := conn_upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	// configure websocket connection
	conn.SetReadLimit((int64(appVars.config.UploadChunkSizeKB) * 1024) + 4096)

	// kick off wsConnHandler so that we can use channels to send and receive data
	wsR, wsW := wsConnHandler(conn)
	defer close(wsW)

	// make a write op return channel and define nifty shorthands for sending
	// and receiving JSON encoded objects (they work like the ones in the
	// websocket package)
	chWriteRet := make(chan error)
	sendJSON := func(v interface{}) error {
		msg, err := json.Marshal(v)
		if err != nil {
			return err
		}
		//log.Printf("send: %+v", string(msg))
		cmd := wsWriteCmd{websocket.TextMessage, msg, chWriteRet}
		wsW <- &cmd
		err = <-chWriteRet
		return err
	}
	recvJSON := func(v interface{}) error {
		recv, ok := <-wsR
		if !ok {
			return errors.New("recvJSON: socket read channel was closed")
		}
		err := json.Unmarshal(recv.data, v)
		//log.Printf("recv str: %s", string(recv.data))
		//log.Printf("recv: %+v", v)
		return err
	}

	// receive upload request from sender
	req := new(MsgUploadReq)
	err = recvJSON(req)
	if err != nil {
		log.Printf("Couldn't read upload request from %s: %s",
			conn.RemoteAddr().String(), err.Error())
		_ = sendJSON(MsgError{Msg: "Couldn't read upload request"})
		_ = closeWebsocketNormally(conn, "")
		return
	}

	// get uploader for requested upload id
	uploader, exists := appVars.uploaders.Get(req.Id)
	if !exists {
		log.Printf("Received upload req from %s for non-existing upload %s",
			conn.RemoteAddr().String(), req.Id)
		_ = sendJSON(MsgError{Msg: "Unknown upload id"})
		_ = closeWebsocketNormally(conn, "")
		return
	}

	// TODO make sure here that uploader is in a legal state

	// make sure we're the only websocket handler to use that upload
	err = uploader.BindToSocketHandler()
	if err != nil {
		log.Printf("Uploader requested by %s already in use by another websocket handler",
			conn.RemoteAddr().String())
		_ = sendJSON(MsgError{Msg: "Another websocket connection already deals with this upload"})
		_ = closeWebsocketNormally(conn, "")
		return
	}
	defer uploader.UnbindFromSocketHandler()

	// if upload is new (not resumed), set fie size. Otherwise, make sure
	// filesize from the request is the same as in uploader (the file on the
	// client side might have changed...)
	if uploader.GetState() == upload.StateInit {
		err = uploader.SetFileSize(req.LengthBytes)
		if err != nil {
			log.Printf("File size from %s is problematic: %s",
				conn.RemoteAddr().String(), err.Error())
			_ = sendJSON(MsgError{Msg: "File probably too large"})
			_ = closeWebsocketNormally(conn, "")
			return
		}
	} else {
		if req.LengthBytes != uploader.GetFileSize() {
			log.Printf("File size from %s has changed",
				conn.RemoteAddr().String())
			_ = sendJSON(MsgError{Msg: "File size has changed"})
			_ = closeWebsocketNormally(conn, "")
			return
		}
	}

	// prepare upload config message
	uploadConf := new(MsgUploadConf)
	uploadConf.ChunkSizeKB = appVars.config.UploadChunkSizeKB
	uploadConf.FilePos = uploader.GetFilePos()
	uploadConf.SendAhead = appVars.config.UploadSendAhead

	// send upload config to sender
	err = sendJSON(uploadConf)
	if err != nil {
		log.Printf("Couldn't send upload config to %s",
			conn.RemoteAddr().String())
		_ = sendJSON(MsgError{Msg: "Couldn't send upload config"})
		_ = closeWebsocketNormally(conn, "")
		return
	}

	// receive ack from sender - from then, all that comes from sender is
	// binary file chunks
	ack := new(MsgAck)
	err = recvJSON(ack)
	if err != nil {
		log.Printf("Didn't receive ack from %s",
			conn.RemoteAddr().String())
		_ = sendJSON(MsgError{Msg: "Didn't receive ack"})
		_ = closeWebsocketNormally(conn, "")
		return
	}
	if !ack.Ack {
		// Sender won't send anything... this upload has failed for now
		// Note that this shouldn't happen in the current implementation
		log.Printf("Got nack from %s right before chunk transfers",
			conn.RemoteAddr().String())
		_ = sendJSON(MsgError{Msg: "you nack-ed"})
		_ = closeWebsocketNormally(conn, "you nack-ed")
		return
	}

	// receive and acknowledge messages with file chunks, pass chunks on to
	// uploader until whole file is here
	for uploader.GetFilePos() != uploader.GetFileSize() {
		recv := <-wsR
		// did the read from the socket go well?
		if recv.err != nil {
			log.Printf("Receive of file chunk or cancel or error or pause from %s failed",
				conn.RemoteAddr().String())
			_ = sendJSON(MsgError{Msg: "Receive of file chunk failed"})
			_ = closeWebsocketNormally(conn, "")
			return
		}
		// did we receive an error. pause, or cancel message?
		if recv.messageType == websocket.TextMessage {
			// TODO we got a cancel or error or pause message. define and
			// handle that stuff
			log.Printf("Got a text message now from %s but I don't handle that yet",
				conn.RemoteAddr().String())
			_ = sendJSON(MsgError{Msg: "Unhandled text message"})
			_ = closeWebsocketNormally(conn, "")
			return
		}
		// did we receive something we don't understand?
		if recv.messageType != websocket.BinaryMessage {
			log.Printf("Expected file chunk or text but got sth else from %s",
				conn.RemoteAddr().String())
			_ = sendJSON(MsgError{Msg: "Expected file chunk or text but got sth else"})
			_ = closeWebsocketNormally(conn, "")
			return
		}
		// still here? fine. consume the file chunk, and when that went well, ack
		err = uploader.ConsumeFileChunk(recv.data)
		if err != nil {
			log.Printf("uploader couldn't consume file chunk: %s",
				err.Error())
			errMsg := fmt.Sprintf("Error while consuming file chunk: %s", err.Error())
			_ = sendJSON(MsgError{Msg: errMsg})
			_ = closeWebsocketNormally(conn, "")
			if uploader.GetState() != upload.StateCancelled {
				go uploader.Cancel(true, errMsg,
					time.Duration(appVars.config.HandoverTimeoutS)*time.Second)
			}
			return
		}
		err = sendJSON(MsgChunkAck{ChunkSize: int64(len(recv.data))})
	}

	// notify web app backend that file is ready to be fetched / moved
	ch_wait := uploader.HandFileToApp(
		time.Duration(appVars.config.HandoverTimeoutS)*time.Second,
		time.Duration(appVars.config.HandoverConfirmTimeoutS)*time.Second)

	// wait until uploader is finished.
	for cont := true; cont; {
		select {
		case recv, ok := <-wsR:
			// if this is an error (probably due to socket being closed), we are
			// just done here.
			if !ok || recv.err != nil {
				log.Printf("lost connection to %s while waiting for file handover to app",
					uploader.GetSignalFinishURL().String())
				return
			}
			// TODO handle cancel or error message
		case err = <-ch_wait:
			//log.Printf("read wait channel: %+v", err)
			cont = false
		}
	}
	if err != nil {
		log.Printf("uploader couldn't hand file over to the application at %s",
			uploader.GetSignalFinishURL().String())
		_ = sendJSON(MsgError{Msg: "Couldn't hand file over to the application"})
		_ = closeWebsocketNormally(conn, "")
		return
	}

	// when uploader is finished, send final "upload is finished" message to app
	// frontend
	if uploader.GetState() == upload.StateFinished {
		err = sendJSON(MsgAllDone{true})
	} else {
		err = sendJSON(MsgError{Msg: "upload cancelled"})
	}
	if err != nil {
		log.Printf("Couldn't send 'all done' to %s",
			conn.RemoteAddr().String())
		_ = closeWebsocketNormally(conn, "")
		return
	}

	// done! finally, close the websocket nicely and let uploader clean up
	err = closeWebsocketNormally(conn, "")
	_ = uploader.CleanUp()
	return
}