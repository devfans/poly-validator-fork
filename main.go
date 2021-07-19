package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"poly-validator/validator"

	"github.com/beego/beego/v2/core/logs"
	"github.com/urfave/cli/v2"
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

	wg := &sync.WaitGroup{}
	ctx, cancel := context.WithCancel(context.Background())
	err = new(validator.Listener).Start(config, ctx, wg, c.Int("chain"))
	if err != nil {
		return err
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	sig := <-sc
	logs.Info("Validator is exiting with received signal:(%s).", sig.String())
	cancel()
	wg.Wait()
	return nil
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
			&cli.IntFlag{
				Name:  "chain",
				Usage: "chain to monitor, default: all cchains specified in config file",
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
