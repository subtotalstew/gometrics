package main

import "os"

func helper() {
	// Не main — анализатор не должен срабатывать.
	os.Exit(1)
}

func main() {
	_ = os.Args
	helper()
}
