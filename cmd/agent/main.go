package main

import (
	"github.com/subtotalstew/gometrics.git/internal/agent"
)

func main() {
	serverAddr := "http://localhost:8080"
	pollInterval := 2
	reportInterval := 10

	a := agent.NewAgent(serverAddr, pollInterval, reportInterval)
	a.Run()
}
