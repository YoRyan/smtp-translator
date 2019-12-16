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

type AuthCombo struct {
	user string
	pw   string
}

func ReadAuth(fd *os.File) (auths []AuthCombo, err error) {
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		split := strings.Split(scanner.Text(), ":")
		if len(split) == 2 {
			auths = append(auths, AuthCombo{
				user: split[0],
				pw:   split[1]})
		}
	}
	err = scanner.Err()
	return
}

func AuthPlaintext(db []AuthCombo, user, pw string) bool {
	for _, ac := range db {
		if user == ac.user && pw == ac.pw {
			return true
		}
	}
	return false
}

func AuthCramMd5(db []AuthCombo, user string, mac, chal []byte) (bool, error) {
	// https://en.wikipedia.org/wiki/CRAM-MD5#Protocol
	rec := make([]byte, hex.DecodedLen(len(mac)))
	n, err := hex.Decode(rec, mac)
	if err != nil {
		return false, err
	}
	rec = rec[:n]
	for _, ac := range db {
		if user == ac.user {
			mac := hmac.New(md5.New, []byte(ac.pw))
			mac.Write(chal)
			exp := mac.Sum(nil)
			if hmac.Equal(exp, rec) {
				return true, nil
			}
		}
	}
	return false, nil
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

func main() {
	addr := flag.String("listen", ":25", "address:port to listen on")
	authp := flag.String("auth", "", "authenticate senders with username:password combinations from `file`")
	host := flag.String("hostname", "smtp-translator", "SMTP server hostname")
	flag.Parse()
	errlog := log.New(os.Stderr, "", 0)
	token, ok := os.LookupEnv("PUSHOVER_TOKEN")
	if !ok {
		errlog.Println("missing env: $PUSHOVER_TOKEN")
		return
	}
	user, ok := os.LookupEnv("PUSHOVER_USER")
	if !ok {
		errlog.Println("missing env: $PUSHOVER_USER")
		return
	}
	var authl []AuthCombo
	if *authp != "" {
		authf, err := os.Open(*authp)
		if err != nil {
			errlog.Println("couldn't read auth file:", err)
			return
		}
		authl, err = ReadAuth(authf)
		if err != nil {
			errlog.Println("couldn't read auth file:", err)
			return
		}
	}

	q := make(chan *Envelope, 10)
	push := pushover.New(token)
	pushRcpt := pushover.NewRecipient(user)
	if _, err := push.GetRecipientDetails(pushRcpt); err != nil {
		errlog.Println(err)
		return
	}
	go func() {
		for {
			var e *Envelope = <-q
			for {
				err, retry := SendPushover(e, push, pushRcpt)
				if err != nil && retry {
					errlog.Println(err, "(retrying in 10 seconds)")
					time.Sleep(10 * time.Second)
					continue
				} else if err != nil {
					errlog.Println(err, "(not recoverable)")
				}
				break
			}
		}
	}()
	server := smtpd.Server{
		Addr:         *addr,
		AuthRequired: len(authl) > 0,
		Hostname:     *host,
		AuthHandler: func(remoteAddr net.Addr, mechanism string, username []byte, password []byte, shared []byte) (bool, error) {
			if len(authl) <= 0 {
				return true, nil
			}
			if mechanism == "PLAIN" || mechanism == "LOGIN" {
				return AuthPlaintext(authl, string(username), string(password)), nil
			} else if mechanism == "CRAM-MD5" {
				/* username = username
				   password = hmac
				   shared   = challenge
				   (see github.com/mhale/smtpd/smtpd.go) */
				return AuthCramMd5(authl, string(username), password, shared)
			} else {
				panic(mechanism)
			}
		},
		Handler: func(remoteAddr net.Addr, from string, to []string, data []byte) {
			var e Envelope
			e.From = from
			e.To = to
			msg, err := mail.ReadMessage(bytes.NewReader(data))
			if err != nil {
				return
			}
			e.Msg = msg
			q <- &e
		}}
	if err := server.ListenAndServe(); err != nil {
		errlog.Println(err)
	}
}
