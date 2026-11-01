package choppie

import _ "github.com/mattn/go-sqlite3"

type operationsConfig struct {
	natsURL        string
	commandAddress string
	pollingDB      string
	newsDB         string
	masterPath     string
	httpAddress    string
	stage          string
	limit          int
	verbose        bool
}
