package main

import (
	"log"
	"time"
)

func main() {
	ch := make(chan bool)

	ticker := time.NewTicker(1 * time.Second)

	go func() {
		for {
			<-ticker.C
			log.Printf("tick %d\n", time.Now().UnixNano())
		}
	}()

	<-ch
}
