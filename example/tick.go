package main

import "time"

func main() {
	ch := make(chan bool)

	ticker := time.NewTicker(1 * time.Second)

	go func() {
		for {
			<-ticker.C
			println("tick")
		}
	}()

	<-ch
}
