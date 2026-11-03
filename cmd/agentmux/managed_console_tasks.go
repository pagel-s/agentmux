package main

type consoleTaskPage struct {
	Recipient, State string
	Query            string
	After            string
	Cursor           string
	More             bool
	Epoch            uint64
}
