package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"

	"github.com/urfave/cli/v2"
	"poly-validator/validator"
)

func start(c *cli.Context) error {
	file := c.String("config")
	if file == "" {
		file = "./config.json"
	}

	fo, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fo.Close()
	bytes, err := ioutil.ReadAll(fo)
	if err != nil {
		return err
	}
	var config validator.Config
	err = json.Unmarshal(bytes, &config)

	if err != nil {
		return err
	}
	return new(validator.Listener).Start(config)
}

func main() {

	app := &cli.App{
		Name:   "Poly Validator",
		Usage:  "Poly cross chain transaction validator",
		Action: start,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "config",
				Value: "config.json",
				Usage: "configuration file",
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
