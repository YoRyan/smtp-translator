/*
The MIT License (MIT)

Copyright (c) 2019 Ryan Young

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/gregdel/pushover"
	"github.com/mhale/smtpd"
)

func ReadAuth(fd *os.File) (db map[string]string, err error) {
	db = make(map[string]string)
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		split := strings.Split(scanner.Text(), ":")
		if len(split) == 2 {
			db[split[0]] = split[1]
		}
	}
	err = scanner.Err()
	return
}

func AuthPlaintext(db map[string]string, user, pw string) bool {
	return db[user] != "" && db[user] == pw
}

func AuthCramMd5(db map[string]string, user string, mac, chal []byte) (bool, error) {
	if db[user] == "" {
		return false, nil
	}
	// https://en.wikipedia.org/wiki/CRAM-MD5#Protocol
	rec := make([]byte, hex.DecodedLen(len(mac)))
	n, err := hex.Decode(rec, mac)
	if err != nil {
		return false, err
	}
	rec = rec[:n]
	mymac := hmac.New(md5.New, []byte(db[user]))
	mymac.Write(chal)
	exp := mymac.Sum(nil)
	return hmac.Equal(exp, rec), nil
}

type Envelope struct {
	From string
	To   []string
	Msg  *mail.Message
}

func SendPushover(e *Envelope, api *pushover.Pushover, dest *pushover.Recipient) (err error, retryable bool) {
	sub := e.Msg.Header.Get("Subject")
	if sub == "" {
		sub = "(no subject)"
	}
	title := sub + " (" + e.From + " to " + strings.Join(e.To, ", ") + ")"
	body, err := ioutil.ReadAll(e.Msg.Body)
	if err != nil {
		retryable = false
		return
	}

	push := pushover.NewMessageWithTitle(string(body), title)
	resp, err := api.SendMessage(push, dest)
	if err != nil {
		retryable = resp != nil && resp.Status != 1
		return
	}
	retryable = false
	return
}

type Config struct {
	Addr     string
	AuthDb   map[string]string
	Hostname string

	PushoverToken string
	PushoverRcpt  string
}

func Run(c *Config, errl *log.Logger) error {
	q := make(chan *Envelope, 10)
	api := pushover.New(c.PushoverToken)
	rcpt := pushover.NewRecipient(c.PushoverRcpt)
	if _, err := api.GetRecipientDetails(rcpt); err != nil {
		return err
	}
	go func() {
		for {
			var e *Envelope = <-q
			for {
				err, retry := SendPushover(e, api, rcpt)
				if err != nil && retry {
					errl.Println(err, "(retrying in 10 seconds)")
					time.Sleep(10 * time.Second)
					continue
				} else if err != nil {
					errl.Println(err, "(not recoverable)")
				}
				break
			}
		}
	}()
	server := smtpd.Server{
		Addr:         c.Addr,
		Appname:      "smtp-translator",
		AuthRequired: len(c.AuthDb) > 0,
		Hostname:     c.Hostname,
		AuthHandler: func(remoteAddr net.Addr, mechanism string, username []byte, password []byte, shared []byte) (bool, error) {
			if len(c.AuthDb) <= 0 {
				return true, nil
			}
			switch mechanism {
			case "PLAIN":
			case "LOGIN":
				return AuthPlaintext(c.AuthDb, string(username), string(password)), nil
			case "CRAM-MD5":
				// username = username, password = hmac, shared = challenge
				// (see github.com/mhale/smtpd/smtpd.go)
				return AuthCramMd5(c.AuthDb, string(username), password, shared)
			}
			panic(mechanism)
		},
		Handler: func(remoteAddr net.Addr, from string, to []string, data []byte) {
			msg, err := mail.ReadMessage(bytes.NewReader(data))
			if err != nil {
				return
			}
			q <- &Envelope{
				From: from,
				To:   to,
				Msg:  msg}
		}}
	return server.ListenAndServe()
}

func main() {
	errl := log.New(os.Stderr, "", 0)
	c, err := getConfig()
	if err != nil {
		errl.Println(err)
		return
	}
	errl.Println(Run(c, errl))
}

func getConfig() (*Config, error) {
	addr := flag.String("listen", ":25", "address:port to listen on")
	authp := flag.String("auth", "", "authenticate senders with username:password combinations from `file`")
	host := flag.String("hostname", "smtp-translator", "SMTP server hostname")
	flag.Parse()
	token, ok := os.LookupEnv("PUSHOVER_TOKEN")
	if !ok {
		return nil, errors.New("missing env: $PUSHOVER_TOKEN")
	}
	user, ok := os.LookupEnv("PUSHOVER_USER")
	if !ok {
		return nil, errors.New("missing env: $PUSHOVER_USER")
	}
	var authdb map[string]string
	if *authp != "" {
		authf, err := os.Open(*authp)
		if err != nil {
			return nil, err
		}
		authdb, err = ReadAuth(authf)
		if err != nil {
			return nil, err
		}
	}
	return &Config{
		Addr:     *addr,
		AuthDb:   authdb,
		Hostname: *host,

		PushoverToken: token,
		PushoverRcpt:  user}, nil
}
