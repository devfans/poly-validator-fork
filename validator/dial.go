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

package validator

import (
	"crypto/md5"
	"encoding/hex"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/beego/beego/v2/core/logs"
)

func Dial(target, content string) error {
	v := url.Values{}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	h := md5.New()
	h.Write([]byte(CONFIG.HuyiAccount + CONFIG.HuyiPassword + target + content + now))
	v.Set("account", CONFIG.HuyiAccount)
	v.Set("password", hex.EncodeToString(h.Sum(nil)))
	v.Set("mobile", target)
	v.Set("content", content)
	v.Set("time", now)
	//body := ioutil.NopCloser(strings.NewReader(v.Encode())) //把form数据编下码
	body := strings.NewReader(v.Encode())
	client := &http.Client{}
	req, err := http.NewRequest("POST", CONFIG.HuyiUrl, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")
	resp, err := client.Do(req)
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	logs.Info("Dail to %s for %s success %s", target, content, string(data))
	return nil
}

func Notify(content string) {
	for _, target := range CONFIG.DialTargets {
		go Dial(target, content)
	}
}
