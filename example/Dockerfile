FROM golang:alpine
ENV GOPATH=/go
COPY go.mod tick.go /go/github.com/narcolepticsnowman/skavo/example/
WORKDIR /go/github.com/narcolepticsnowman/skavo/example/
RUN go install -gcflags="all=-N -l" github.com/narcolepticsnowman/skavo/tick

CMD /go/bin/tick
