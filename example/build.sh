#!/usr/bin/env bash
scriptDir=$(dirname "$0")
env GOOS=linux GOARCH=386 go build -o tick "$scriptDir/tick.go"
docker build . -t narcolepticsnowman/tick
