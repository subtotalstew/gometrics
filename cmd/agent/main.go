package main

import (
	"flag"

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

	a := agent.NewAgent("http://"+addr, pollInterval, reportInterval)
	a.Run()
}
