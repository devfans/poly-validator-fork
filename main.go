package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
)

func start(c *cli.Context) error {
	return nil
}

func main() {

	app := &cli.App{
		Name:   "Poly Validator",
		Usage:  "Poly cross chain transaction validator",
		Action: start,
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
