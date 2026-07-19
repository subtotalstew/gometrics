package main

import (
	"flag"
	"log"
	"os"
	"strconv"

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

	if envAddr := os.Getenv("ADDRESS"); envAddr != "" {
		addr = envAddr
	}

	if envPoll := os.Getenv("POLL_INTERVAL"); envPoll != "" {
		val, err := strconv.Atoi(envPoll)
		if err != nil {
			log.Fatalf("неверный формат POLL_INTERVAL: %v", err)
		}
		pollInterval = val
	}

	if envReport := os.Getenv("REPORT_INTERVAL"); envReport != "" {
		val, err := strconv.Atoi(envReport)
		if err != nil {
			log.Fatalf("неверный формат REPORT_INTERVAL: %v", err)
		}
		reportInterval = val
	}

	a := agent.NewAgent("http://"+addr, pollInterval, reportInterval)
	a.Run()
}
