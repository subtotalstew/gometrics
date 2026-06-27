package main

import (
	"flag"
	"log"

	"github.com/subtotalstew/gometrics.git/internal/agent"
)

func main() {
	var (
		addr           string
		pollInterval   int
		reportInterval int
	)

	flag.StringVar(&addr, "a", "localhost:8080", "server address")
	flag.IntVar(&pollInterval, "p", 2, "poll interval in seconds")
	flag.IntVar(&reportInterval, "r", 10, "report interval in seconds")

	flag.Parse()

	if flag.NFlag() > 3 {
		flag.Usage()
		log.Fatal("Check startup arguments!!...startup Failed.")
	}
	a := agent.NewAgent("http://"+addr, pollInterval, reportInterval)
	a.Run()
}
