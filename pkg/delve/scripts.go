package delve

const (
	installDelve = `
#!/bin/sh
set -e
check(){
	which $1 2>&1 >/dev/null
}
if [ -z "$GOPATH" ]; then
	export GOPATH=/go
fi
if [ -f $GOPATH/bin/dlv ];then
	echo "Delve Already Installed"
else
	if ! check git || ! check wget || ! check ssh ; then
		if check apk; then
			apk add --no-cache git wget openssh >/dev/null 2>&1
		elif check apt-get; then
			apt-get update && apt-get install -y git wget openssh >/dev/null 2>&1
		elif check dnf; then
			dnf -y install git wget openssh >/dev/null 2>&1
		elif check yum; then
			yum update && yum install -y git wget openssh >/dev/null 2>&1
		else
			echo "Can't install requirements'"
			exit 1
		fi
	fi
	if ! check go; then
		if check apk ; then
		  #alpine is special and the regular linux binaries don't work
		  apk update && apk add --no-cache go > /dev/null 2>&1
		  go version
		else
			if [ ! -f /usr/lib/go/bin/go ]; then
				wget -qO - https://dl.google.com/go/go1.16.linux-amd64.tar.gz | tar -xz -C /usr/lib
			fi
			if [ ! -f /bin/go ]; then
				ln -s /usr/lib/go/bin/go /bin/go
			fi
			go version
		fi
	fi
	mkdir -p $GOPATH/src/github.com/go-delve
	cd $GOPATH/src/github.com/go-delve
	git clone https://github.com/go-delve/delve
	cd delve/cmd/dlv
	go build
	mkdir -p $GOPATH/bin
	mv dlv $GOPATH/bin/dlv
fi
`
	doInstallDelve = `
#!/bin/sh
rm /tmp/skavo/installsuccess /tmp/skavo/installfail 2>/dev/null
if sh /tmp/skavo/installDelve.sh > /tmp/skavo/install.log 2>&1 ;then
	touch /tmp/skavo/installsuccess
else
	touch /tmp/skavo/installfail
fi
`
	skavoEntrypoint = installDelve + `
port=$1
shift
echo "Skavo Starting: $@"
delve=$(which dlv 2>/dev/null || echo $GOPATH/bin/dlv)
$delve --headless --listen=:$port --api-version=2 --accept-multiclient exec "$@" 2>&1 </dev/null  &
`
	delveAttach = `
#!/bin/sh
if [ -z "$GOPATH" ]; then
	export GOPATH=/go
fi
find $GOPATH
if ! ps -ef |grep -v grep|grep -q "$GOPATH/bin/dlv --headless"  ; then
	$GOPATH/bin/dlv --headless --listen=:$1 --api-version=2 --accept-multiclient attach $2 2>&1 &
else
	echo "Delve already attached"
fi
`
	delveExec = `
#!/bin/sh
if [ -z "$GOPATH" ]; then
	export GOPATH=/go
fi
if ! ps -ef |grep -v grep|grep -q "$GOPATH/bin/dlv --headless"  ; then
	port=$1
	pid=$2
	shift 2
	echo "Restarting: $pid, $@"
	kill $pid
	$delve --headless --listen=:$port --api-version=2 --accept-multiclient exec "$@" 2>&1 </dev/null  &
else
	echo "Delve already attached"
fi
`
)
