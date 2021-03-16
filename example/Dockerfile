FROM golang:alpine
ENV GOPATH=/go
COPY go.mod tick.go /go/github.com/ncsnw/skavo/example/
WORKDIR /go/github.com/ncsnw/skavo/example/
RUN go install -gcflags="all=-N -l" github.com/ncsnw/skavo/tick

CMD /go/bin/tick
