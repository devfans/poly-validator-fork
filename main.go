/*
 * Copyright (C) 2021 The poly network Authors
 * This file is part of The poly network library.
 *
 * The  poly network  is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The  poly network  is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 * You should have received a copy of the GNU Lesser General Public License
 * along with The poly network .  If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"poly-validator/validator"

	"github.com/beego/beego/v2/core/logs"
	"github.com/beego/beego/v2/server/web"
	"github.com/polynetwork/bridge-common/chains/poly"
	"github.com/polynetwork/bridge-common/metrics"
	"github.com/polynetwork/bridge-common/tools"
	"github.com/urfave/cli/v2"
)

func parseConfig(c *cli.Context) (*validator.Config, error) {
	file := c.String("config")
	if file == "" {
		file = "./config.json"
	}

	fo, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fo.Close()
	bytes, err := ioutil.ReadAll(fo)
	if err != nil {
		return nil, err
	}
	var config validator.Config
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse config file %w", err)
	}
	return &config, nil
}

func start(c *cli.Context) error {
	config, err := parseConfig(c)
	if err != nil {
		panic(err)
	}

	go func() {
		// Insert web config
		web.BConfig.Listen.HTTPAddr = config.MetricHost
		web.BConfig.Listen.HTTPPort = config.MetricPort
		web.BConfig.RunMode = "prod"
		web.BConfig.AppName = "validator"
		web.Run()
	}()
	tools.DingUrl = config.DingUrl
	metrics.Init("validator")

	wg := &sync.WaitGroup{}
	ctx, cancel := context.WithCancel(context.Background())
	err = new(validator.Listener).Start(*config, ctx, wg, c.Int("chain"))
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

func fetchMerkle(c *cli.Context) error {
	config, err := parseConfig(c)
	if err != nil {
		panic(err)
	}

	poly, err := poly.NewSDK(0, config.PolyNodes, 10, 0)
	if err != nil {
		return err
	}
	height := c.Int("poly_height")
	return validator.ScanPolyProofs(uint64(height), poly, config.PolyCCMContract)
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
		Commands: []*cli.Command{
			{
				Name:    "poly_merkle",
				Aliases: []string{"m"},
				Usage:   "fetch merkle values from poly",
				Action:  fetchMerkle,
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "poly_height",
						Usage: "Poly block height to scan against",
					},
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
