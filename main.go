// Copyright (c) 2019-2020 Ryan Young
//
// The MIT License (MIT)
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

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
	"mime"
	"net"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gregdel/pushover"
	"github.com/mhale/smtpd"
)

// Pushover API limits per https://pushover.net/api#limits
const MaxEmailLength = 1024
const MaxTitleLength = 250
const MaxUrlLength = 512
const MaxUrlTitleLength = 100

// An Envelope represents an email that is finalized, parsed, and ready for
// submission.
type Envelope struct {
	From    *Sender
	To      *Recipient
	Subject string
	Body    string
}

// A Sender represents the source Pushover app token and the original email
// From: address. In the future, there may be additional fields.
type Sender struct {
	AppToken    string
	Address     string
	ShowAddress bool
}

// A Recipient represents a valid Pushover destination with optional
// fields to customize the notification.
type Recipient struct {
	UserToken string
	Device    string
	Priority  int
	Sound     string
}

// SendPushover converts an Envelope into a Pushover notification. In the event
// of an error condition, retryable indicates whether or not the Envelope can be
// resent.
func SendPushover(e *Envelope, api *pushover.Pushover) (retryable bool, err error) {
	if e.From.AppToken == "" || e.To.UserToken == "" {
		retryable = false
		return
	}
	rcpt := pushover.NewRecipient(e.To.UserToken)
	_, err = api.GetRecipientDetails(rcpt)
	if err != nil {
		retryable = false
		return
	}

	sub := e.Subject
	if sub == "" {
		sub = "(no subject)"
	}
	var title string
	if e.From.ShowAddress {
		title = sub + " (" + e.From.Address + ")"
	} else {
		title = sub
	}

	push := &pushover.Message{
		Message:    truncate(e.Body, MaxEmailLength),
		Title:      truncate(title, MaxTitleLength),
		Priority:   e.To.Priority,
		DeviceName: e.To.Device,
		Sound:      e.To.Sound,
	}
	resp, err := api.SendMessage(push, rcpt)
	if err != nil {
		retryable = resp != nil && resp.Status != 1
		return
	}
	retryable = false
	return
}

func truncate(s string, maxLength int) string {
	if len(s) >= maxLength {
		return s[0:maxLength-4] + "..."
	} else {
		return s
	}
}

// Config holds all parameters for SMTP Translator.
type Config struct {
	Addr        string
	AuthDb      map[string]string
	Hostname    string
	TLSCert     string
	TLSKey      string
	Starttls    bool
	StarttlsReq bool

	AppToken   string
	MultiToken bool
}

// ListenAndServe runs an instance of SMTP Translator. It takes a server
// configuration and a logger for non-fatal errors.
func ListenAndServe(c *Config, errl *log.Logger) error {
	q := make(chan *Envelope, 10)
	server := smtpd.Server{
		Addr:         c.Addr,
		Appname:      "SMTP-Translator",
		AuthRequired: len(c.AuthDb) > 0,
		Hostname:     c.Hostname,
		TLSListener:  !c.Starttls && !c.StarttlsReq,
		TLSRequired:  c.StarttlsReq,
		AuthHandler: func(remoteAddr net.Addr, mechanism string, username []byte, password []byte, shared []byte) (bool, error) {
			if len(c.AuthDb) <= 0 {
				return true, nil
			}
			switch mechanism {
			case "PLAIN", "LOGIN":
				return authPlaintext(c.AuthDb, string(username), string(password)), nil
			case "CRAM-MD5":
				// username = username, password = hmac, shared = challenge
				// (see github.com/mhale/smtpd/smtpd.go)
				return authCramMd5(c.AuthDb, string(username), password, shared)
			}
			panic(mechanism)
		},
		HandlerRcpt: func(remoteAddr net.Addr, from string, to string) bool {
			return parseRecipient(to).UserToken != ""
		},
		Handler: func(remoteAddr net.Addr, from string, to []string, data []byte) {
			parsedSndr := parseSender(from)
			if !c.MultiToken {
				parsedSndr.AppToken = c.AppToken
				parsedSndr.ShowAddress = true
			}

			msg, err := mail.ReadMessage(bytes.NewReader(data))
			if err != nil {
				errl.Println("malformed email message:", err)
				return
			}
			for _, rcpt := range to {
				parsedRcpt := parseRecipient(rcpt)
				if parsedRcpt.UserToken != "" {
					q <- makeEnvelope(parsedSndr, parsedRcpt, msg)
				} else {
					errl.Println("bad address:", rcpt)
				}
			}
		}}
	if c.TLSCert != "" && c.TLSKey != "" {
		if err := server.ConfigureTLS(c.TLSCert, c.TLSKey); err != nil {
			return err
		}
	}
	go func() {
		for {
			var e *Envelope = <-q
			for {
				api := pushover.New(e.From.AppToken)
				retry, err := SendPushover(e, api)
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
	return server.ListenAndServe()
}

func authPlaintext(db map[string]string, user, pw string) bool {
	return db[user] != "" && db[user] == pw
}

// authCramMd5 implements the CRAM-MD5 SMTP authentication method, which compares
// a user-submitted HMAC with an expected HMAC that is derived from a shared
// secret (in SMTP Translator's case, the plaintext password).
func authCramMd5(db map[string]string, user string, mac, chal []byte) (bool, error) {
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

func parseSender(addr string) (sndr *Sender) {
	var s Sender
	sndr = &s

	s.Address = addr
	app := findStringSubmatch(`(a\w+)@`, addr)
	if len(app) == 0 {
		return
	}
	s.AppToken = app[1]
	return
}

func parseRecipient(addr string) (rcpt *Recipient) {
	var r Recipient
	rcpt = &r

	user := findStringSubmatch(`^(u\w+)((?:>[\w,]+|#[-\+]?\d|!\w+)*)@`, addr)
	if len(user) == 0 {
		return
	}
	r.UserToken = user[1]
	if len(user) == 1 {
		return
	}
	opts := user[2]

	device := findStringSubmatch(`>([\w,]+)`, opts)
	if len(device) == 2 {
		r.Device = device[1]
	}

	priority := findStringSubmatch(`#([-\+]?\d)`, opts)
	if len(priority) == 2 {
		r.Priority, _ = strconv.Atoi(priority[1])
	}

	sound := findStringSubmatch(`!(\w+)`, opts)
	if len(sound) == 2 {
		r.Sound = sound[1]
	}

	return
}

// makeEnvelope extracts plaintext versions of the Message's subject and body,
// as well as (in the near future) binary versions of any attachments.
func makeEnvelope(sndr *Sender, rcpt *Recipient, m *mail.Message) *Envelope {
	wordDec := new(mime.WordDecoder)

	subjectRaw := m.Header.Get("Subject")
	subject, err := wordDec.Decode(subjectRaw)
	if err != nil {
		subject = subjectRaw
	}

	bodyBytes, err := ioutil.ReadAll(m.Body)
	var bodyRaw string
	if err != nil {
		bodyRaw = ""
	} else {
		bodyRaw = string(bodyBytes)
	}
	body, err := wordDec.Decode(bodyRaw)
	if err != nil {
		body = bodyRaw
	}

	return &Envelope{
		From:    sndr,
		To:      rcpt,
		Subject: subject,
		Body:    body}
}

func findStringSubmatch(re string, s string) []string {
	return regexp.MustCompile(re).FindStringSubmatch(s)
}

func main() {
	errl := log.New(os.Stderr, "", 0)
	c, err := getConfig()
	if err != nil {
		errl.Println(err)
		return
	}
	errl.Println(ListenAndServe(c, errl))
}

func getConfig() (*Config, error) {
	addr := flag.String("addr", ":25",
		"address:port to listen on")
	multi := flag.Bool("multiapp", false,
		"read app tokens from the From: address")
	authp := flag.String("auth", "",
		"authenticate senders with username:password combinations from `file`")
	oshost, err := os.Hostname()
	if err != nil {
		oshost = "localhost"
	}
	host := flag.String("hostname", oshost,
		"advertise an SMTP server hostname")
	tlsCert := flag.String("tls-cert", "",
		"if using TLS, path to TLS certificate file")
	tlsKey := flag.String("tls-key", "",
		"if using TLS, path to TLS key file")
	starttls := flag.Bool("starttls", false,
		"if using TLS, accept unencrypted connections that may upgrade with STARTTLS")
	starttlsReq := flag.Bool("starttls-always", false,
		"if using TLS, accept unencrypted connections that MUST upgrade with STARTTLS")
	flag.Parse()

	if (*tlsCert != "" || *tlsKey != "") && (*tlsCert == "" || *tlsKey == "") {
		return nil, errors.New("must specify both -tls-cert and -tls-key")
	}
	if *starttls && *starttlsReq {
		return nil, errors.New("must specify either -starttls or -starttls-always")
	}
	if (*starttls || *starttlsReq) && (*tlsCert == "" || *tlsKey == "") {
		return nil, errors.New("must specify -tls-cert and -tls-key to use TLS")
	}
	token, ok := os.LookupEnv("PUSHOVER_TOKEN")
	if !*multi && !ok {
		return nil, errors.New("missing env: $PUSHOVER_TOKEN")
	}

	var authdb map[string]string
	if *authp != "" {
		authf, err := os.Open(*authp)
		if err != nil {
			return nil, err
		}
		authdb, err = readAuth(authf)
		authf.Close()
		if err != nil {
			return nil, err
		}
	}

	return &Config{
		Addr:        *addr,
		AuthDb:      authdb,
		Hostname:    *host,
		TLSCert:     *tlsCert,
		TLSKey:      *tlsKey,
		Starttls:    *starttls,
		StarttlsReq: *starttlsReq,

		AppToken:   token,
		MultiToken: *multi}, nil
}

func readAuth(fd *os.File) (db map[string]string, err error) {
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
