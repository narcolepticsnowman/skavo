package delve

import (
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/narcolepticsnowman/skavo/pkg/k8s"
)

type PodDelve struct {
	Namespace     string
	PodName       string
	ContainerName string
	Process       k8s.ContainerProcess
	Client        *k8s.Client
	LocalPort     string
	PodPort       string
}

func (pd *PodDelve) RelaunchPodWithDelve() {
	log.Printf("Relaunching pod %s with %+v running in delve\n", pd.PodName, pd.Process)
	//create startup script config map with epic bash only wget https://unix.stackexchange.com/a/83927/241480
	//In the startup script
	//download go, unpack it, go install delve, execute "$[@]"
	//mount the script to the container
	//replace the entrypoint (or wrap the existing entrypoint) in the pod and re-create it
}

func (pd *PodDelve) Exec(cmd ...string) {

	pd.Client.Exec(
		pd.PodName,
		pd.Namespace,
		pd.ContainerName,
		cmd,
		k8s.ExecOptions{
			Out:    nil,
			In:     nil,
			ErrOut: os.Stderr,
		},
	)
}

func (pd *PodDelve) ExecWrite(in io.Reader, cmd ...string) {
	pd.Client.Exec(
		pd.PodName,
		pd.Namespace,
		pd.ContainerName,
		cmd,
		k8s.ExecOptions{
			Out:    nil,
			In:     in,
			ErrOut: nil,
		},
	)
}

func (pd *PodDelve) runScript(src string, name string, args ...string) {
	pd.ExecWrite(strings.NewReader(src), "sh", "-c", "cat /dev/stdin > /"+name)
	pd.Exec("sh", "-c", "sh /"+name+" "+strings.Join(args, " ")+" 2>&1 </dev/null >${GOPATH:-/}"+name+".log &")
}

func (pd *PodDelve) AttachDelveToProcess() {
	log.Println("Installing Delve...")
	pd.runScript(installDelve, "installDelve.sh")
	log.Printf("Attaching to Process: %+v\n", pd.Process)
	pd.runScript(delveAttach, "delveAttach.sh", pd.PodPort, strconv.Itoa(pd.Process.Pid))
	log.Printf("Delve is running on pod %s\n", pd.PodName)
	log.Printf("Forwarding local port %s to remote port %s\n", pd.LocalPort, pd.PodPort)
	<-pd.Client.ForwardPort(pd.Namespace, pd.PodName, pd.LocalPort, pd.PodPort)
}

const (
	installDelve = `
#!/bin/sh
check(){
	which $1 2>&1 >/dev/null
}
if ! check git || ! check wget; then
	if check apk; then
		apk add --no-cache git
	elif check apt-get; then
		apt-get update && apt-get install -y git wget
	elif check dnf; then
		dnf -y install git wget
	elif check yum; then
		yum update && yum install -y git wget
	else
		echo "Can't install git'"
		exit 1
	fi
fi
if ! check go; then
	if check apk ; then
	  #alpine is special and the regular linux binaries dont' work
	  apk update && apk add --no-cache go
	  go version
	else
	  #Assume another linux flavor
      if [ ! -f /usr/lib/go/bin/go ]; then
	    wget -qO - https://dl.google.com/go/go1.16.linux-amd64.tar.gz | tar -xz -C /usr/lib
	  fi
      if [ ! -f /bin/go ]; then
	    ln -s /usr/lib/go/bin/go /bin/go
	  fi
	  go version
	fi
fi
if [ -z "$GOPATH" ]; then
	export GOPATH=/go
fi
mkdir -p $GOPATH/src/github.com/go-delve
cd $GOPATH/src/github.com/go-delve
git clone https://github.com/go-delve/delve
cd delve
go install github.com/go-delve/delve/cmd/dlv
`
	delveAttach = `
#!/bin/sh
if [ -z "$GOPATH" ]; then
	export GOPATH=/go
fi
if ! ps -ef |grep -v grep|grep -q "$GOPATH/bin/dlv --headless"  ; then
	$GOPATH/bin/dlv --headless --listen=:$1 --api-version=2 --accept-multiclient attach $2 2>&1 >/dlv.log </dev/null  &
else
	echo "Delve already attached"
fi
`
	delveExec = `
#!/bin/sh
if [ -z "$GOPATH" ]; then
	export GOPATH=/go
fi
`
)
