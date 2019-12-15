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
	"bytes"
	"errors"
	"flag"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"time"

	"github.com/bradfitz/go-smtpd/smtpd"
)

// An Envelope for tracking state with smtpd. It populates msg and sends itself
// to ch when closed.
type StoredEnvelope struct {
	body  []byte
	ch    chan *StoredEnvelope
	sndr  smtpd.MailAddress
	rcpts []smtpd.MailAddress
	msg   *mail.Message
}

func NewStoredEnvelope(ch chan *StoredEnvelope, from smtpd.MailAddress) *StoredEnvelope {
	e := new(StoredEnvelope)
	e.ch = ch
	e.sndr = from
	return e
}

func (e *StoredEnvelope) AddRecipient(rcpt smtpd.MailAddress) error {
	e.rcpts = append(e.rcpts, rcpt)
	return nil
}

func (e *StoredEnvelope) BeginData() error {
	if len(e.rcpts) == 0 {
		return smtpd.SMTPError("554 5.5.1 Error: no valid recipients")
	}
	return nil
}

func (e *StoredEnvelope) Write(line []byte) error {
	e.body = append(e.body, line...)
	return nil
}

func (e *StoredEnvelope) Close() error {
	msg, err := mail.ReadMessage(bytes.NewReader(e.body))
	if err != nil {
		return err
	}
	e.msg = msg
	e.ch <- e
	return nil
}

// Get a human-readable list of recipients.
func (e *StoredEnvelope) Recipients() string {
	var s string
	for i, ma := range e.rcpts {
		if i > 0 {
			s += ", "
		}
		s += ma.Email()
	}
	return s
}

// Submit a finalized Envelope to Pushover.
func SendPushover(e *StoredEnvelope, token string, user string) error {
	sub := e.msg.Header.Get("Subject")
	if sub == "" {
		sub = "(no subject)"
	}
	sndr := e.sndr.Email()
	rcpts := e.Recipients()

	resp, err := http.PostForm("https://api.pushover.net/1/messages.json",
		url.Values{
			"token":   {token},
			"user":    {user},
			"message": {string(e.body)},
			"title":   {sub + " (" + sndr + " to " + rcpts + ")"}})
	if err != nil {
		return nil
	}
	if resp.StatusCode != 200 {
		return errors.New("bad response from pushover")
	}
	return nil
}

func main() {
	addr := flag.String("listen", ":25", "address:port to listen on")
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

	q := make(chan *StoredEnvelope, 10)
	go func() {
		for {
			select {
			case e := <-q:
				for {
					err := SendPushover(e, token, user)
					if err == nil {
						break
					}
					errlog.Println(err)
					time.Sleep(30 * time.Second)
				}
			}
		}
	}()
	server := smtpd.Server{
		Addr:      *addr,
		Hostname:  "",
		PlainAuth: false,
		OnNewMail: func(c smtpd.Connection, from smtpd.MailAddress) (smtpd.Envelope, error) {
			return NewStoredEnvelope(q, from), nil
		}}
	err := server.ListenAndServe()
	if err != nil {
		errlog.Println(err)
	}
}
