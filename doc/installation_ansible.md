Automated installations with Ansible
====================================

During development, we used Ansible and Docker to build and deploy Incoming!! and the example web apps on verious machines around here. The resulting roles, playboocks, and Dockerfiles, while not pretty, might serve you well as starting points for your own setup. We also use all of this for the automated setup of the demo Vagrant VM.

In the following, we describe how to use our Ansible stuff to either build and install the Incoming!! server only, or to build and deploy the whole example setup.


Compile and install the Incoming!! server automatically with Ansible
--------------------------------------------------------------------

First you need to look at the inventory file in [ansible/inventory/default](../ansible/inventory/default). Either edit it, or make your own. What you need is a group called 'build' with one host in it. That host needs the variable `host_build_user`: a user account on that machine in whose home directory we can store stuff. The host must also be root-accessible for Ansible. Further, Docker must be installed on that host, as our Ansible role doesn't take care of that yet (sorry - check [ansible/everything\_in\_vagrant\_box.yml](ansible/everything_in_vagrant_box.yml)). If the build host is localhost, you might want to make sure that Ansible uses the SSH connection type and not the local connection type, as per the time of this writing Ansible's 'synchronize' module doesn't work properly on 'local' connections.

Another thing you might want to have a look at is the Ansible config file we're using. It's in [ansible/ansible.cfg](../ansible/ansible.cfg). Note the "transport" option, which ties Ansible down to using SSH connections, not local ones, even for tasks running on localhost.

Unless you want to build Incoming!! *and* the example web apps and reverse proxy (which is described further below), you need to hack the Ansible playbook that makes it all (or copy it and modify the copy). This playbook is in [ansible/build\_and\_run\_incoming\_and\_examples.yml](../ansible/build_and_run_incoming_and_examples.yml). Remove the role invocations for the examples and the example webserver from the 'build' play, and remove the 'test' play entirely. When you run the playbook now, you get a Docker image file stored into the [ansible/docker\_images](../ansible/docker_images) directory. That image can be loaded into a Docker daemon with `docker load`. Tadaa, Incoming!! in a container.

If you want SSH access to Incoming!! Docker containers, copy your public key(s) to the [ansible/authorized\_keys](../ansible/authorized_keys) directory before you run the playbook.

In order to run the Incoming!! server, you need to load the Docker image into the Docker daemon on the machine you want to run the server on, and then just run it. Map port 4000 (Incoming!!'s default port), and optionally port 22 (SSH). Map in a host directory to the container's /var/incoming directory, where uploaded files end up. If you want to see how we do that with Ansible, check the first few tasks in [ansible/roles/incoming\_and\_examples\_on\_one\_host/tasks/main.yml](../ansible/roles/incoming_and_examples_on_one_host/tasks/main.yml). Note that the setup there doesn't map in a specific host directory for /var/incoming but rather accesses that volume from another Docker container running the example web app on the same host.

The Incoming!! server log is in /var/log/incoming.log inside the container. You either have to SSH into the container to check the log, or you have to map in a host directory to /var/log when starting the container if you want to read the log without having to get into the container.

The Incoming!! server is stateless, so we recommend to just discard Docker containers after using them.


Alternative: build and run the whole example setup automatically with Ansible
-----------------------------------------------------------------------------

The Ansible playbook [ansible/build\_and\_run\_incoming\_and\_examples.yml](../ansible/build_and_run_incoming_and_examples.yml) automates the building and running of an Incoming!! server, one of the two example web apps, and a reverse proxy. At present, it is written having the author's test setup in mind. You will have to edit the supplied Ansible inventory file in order to run the playbook on your rig. Also, you need to have Docker installed already on the machines you use for building and running the Docker containers.

The first steps to build everything are the same as for building only the Incoming!! server with Ansible (see above). You need to edit the inventory file and you might want to have a look at the Ansible config. There is only one difference to the above case when it comes to the inventory: in addition to the 'build' group, you also need a 'test' group, in which you place machines that should (each) run the whole combo of Incoming!! server, example web app, and reverse proxy. The machine(s) you put in that group can of course be the same machine as in the 'build' group.


### Configure the example setup

You can configure the setup you are going to get by modifying the playbook and by adding files into certain directories before running the playbook. In the following, we highight the most likely things you might want to do.

There are two example web apps, but only one web app will be served in our example setup. You can configure which web app that will be in the playbook ([ansible/build\_and\_run\_incoming\_and\_examples.yml](../ansible/build_and_run_incoming_and_examples.yml)). in the play that runs on the 'test' hosts, edit the 'example\_port' variable that is passed to the 'incoming\_and\_examples\_on\_one\_host' role. Set that variable to 4001, and example web app 1 will be served. 4002 will serve example 2.

In the same playbook, you can also configure whether you want to be able to access the running containers with SSH from the outside or not. The 'incoming\_\[...\]\_port\_maps' variables set up these port mappings for the incoming and the example web apps containers, respectively.

The containers only permit SSH logins with key-based authentication, so you need to have your public keys installed in the containers. Just copy the public keys you want to have set up in the containers to the [ansible/authorized\_keys](../ansible/authorized\_keys) and/or the [examples/ansible/authorized\_keys](../examples/ansible/authorized\_keys) directories.

If you don't want to be able to SSH directly into the containers from the outside (you probably don't want to be able to in a production setup later), you can still SSH into them, by going through the host on which the Docker containers run. You SSH to the host, and from there into the containers. To disable 'external' SSH logins, modify (empty) the port forwarding variables in the playbook. To be able to SSH 'internally' into the containers, you probably need a private key on the host, and corresponding public keys in the containers. Put private key file(s) into the [ansible/private\_keys](../ansible/private\_keys) and/or the [examples/ansible/private\_keys](../examples/ansible/private\_keys) directories.

If you want an SSL enabled setup, just add SSL certificates to the [ansible/ssl-certs](../ansible/ssl-certs) directory, and your installation on the corresponding host will support HTTPS. In order for this to work, name your files like this: `<fqdn of host>-nginx.pem` for the certificate file (including possible intermediates), and `<fqdn of host>.key` for the key file. 'fqdn' means 'fully qualified domain name', for example 'my-example-installation.my-company.com'. Note: you might want to run `ansible <host> -i <your inventory file> -m setup` and check the `ansible_fqdn` variable to make sure that Ansible figures out the fqdn correctly.


### Notes on running the example setup

To inspect running containers, log in to them using SSH. Incoming!! and example web apps write logs to /var/log/incoming\_example\_\[12\].log and /var/log/incoming in their respective containers.

Note that if you stop and start either the Incoming!! or the web app example container manually, or if the Docker daemon is restarted for some reason, at least at the time of this writing the internal IP addresses of the containers change and the installation will break because the reverse proxy setup is suddenly wrong (it will answer with "502 - Bad Gateway"). This odd behavior might be fixed in Docker at some point. Until then, the easiest way to fix this problem is to just run the playbook again. (This is not a good solution for a production setup, but we're only doing examples here after all.)


Notes on hacking all of this
----------------------------

This is roughly what happens when an Incoming!! container is built automatically: first, the Incoming!! source files and some other stuff are copied over to the build host. Then, a Docker image is built there, using a [Dockerfile](../Dockerfile) we provide. During the build, Ansible is installed and executed *inside* the container (check [ansible/inside-docker.yml](../ansible/inside-docker.yml)). The inside-docker.yml playbook installs Go, builds the Incoming!! server, and installs your SSH keys. Then, the Docker image is exported into a tarball, which is downloaded to the Ansible control host, into the [ansible/docker\_images](../ansible/docker_images) directory.

The example web apps container is made using the same process as the Incoming!! server container, but using a different in-container playbook and of course a different project directory. The reverse proxy container is defined by nothing but a Dockerfile, and configuration is later done with config files that are mapped into the container (check [ansible/roles/incoming\_and\_examples\_on\_one\_host/tasks](../ansible/roles/incoming_and_examples_on_one_host/tasks)).

Deploying and running the containers then roughly works like this: copy the Docker image tarballs to the target host, load them into the Docker container there, then configure and start the containers.

All the '.rsync-filter-\*' files that are scattered throughout the source repository are filter definitions that are passed in to rsync. '.rsync-filter' are filters that are always applied, '-incoming' are filters that are only applied when the Incoming!! container is built, and '-examples' are filters that are only applied when the example web apps container is built.


Back to [main page](../README.md)
