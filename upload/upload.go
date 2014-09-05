package upload

import (
	"net/url"
	"time"
)

const (
	StateInit = iota
	StateUploading
	StatePaused
	StateHandingOver
	StateCancelled
	StateFinished
	StateCleanedUp
)

type Uploader interface {
	// only one websocket connection per uploader
	BindToSocketHandler() error
	UnbindFromSocketHandler() error

	SetFileSize(int64) error
	GetFileSize() int64
	GetFilePos() int64
	GetSignalFinishURL() *url.URL
	GetId() string

	// GetState queries the upload state. It never takes long to return.
	GetState() int

	// ConsumeFileChunk synchronously stores the next file chunk to whichever
	// store the implementation uses.
	// An error is returned if the operation fails. In that case, the write
	// operation 'never happened'. The upload does not cancel automatically.
	ConsumeFileChunk([]byte) error

	// HandFileToApp asynchronously notifies the app backend that a file with a
	// certain id has arrived, and that the app backend can fetch / move / copy
	// it. It then optionally waits until the app backend is finished
	// obtaining the file (whether this wait happens is decided by the app
	// backend). After all of that is done, HandFileToApp sends an error object
	// to the channel the function returns. It's fine if nobody is listening.
	// When everything is done successfully, the state of the upload is either
	// 'finished'.
	//
	// If handover is not successful, the returned error object is not nil, and
	// the upload's state is "cancelled". CleanUp() is not automatically
	// called.
	//
	// HandFileToApp can be called several times while or even after its
	// internal goroutine is running. It will always return the same channel,
	// and write and close that channel eventually. That is to say, for each
	// Uploader, HandFileToApp's functionality runs exactly once.
	//
	// the two parameters are timeouts for the request to the app backend,
	// and waiting for the confirmation request (if there will be any)
	HandFileToApp(time.Duration, time.Duration) chan error

	// HandoverDone should be called by the app backend when it is finished
	// obtaining the file.
	// error is not nil if there was not HandFileToApp routine running, or
	// if the upload was not in the "hand over file" state.
	HandoverDone() error

	// Cancel ends the upload. No new chunks will be accepted.  The first
	// parameter determines whether the app backend should be notified or not.
	// This should be set to true unless Cancel() is called from the app
	// backend itself. The second parameter can hold an error message that will
	// be sent to the app backend. The third parameter specifies a timeout for
	// the http request to the app backend (0 for no timeout). Call Cancel() in
	// its own goroutine if you don't want to wait for it to finish telling the
	// web backend.
	//
	// Cancel() does not automatically call CleanUp().
	//
	// The returned error is not nil either if telling the web app backend was
	// not successful or if the upload can't be cancelled because it is already
	// too far in the process (i.e., if it is in one of the following states:
	// handing over, finished, cleaned up). Cancelling a cancelled upload is a
	// no-op.
	Cancel(bool, string, time.Duration) error

	Pause() error
	CleanUp() error

	// facilitate housekeeping
	GetCreationTime() time.Time
	GetIdleDuration() time.Duration
}
