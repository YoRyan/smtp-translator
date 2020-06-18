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
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"mime/multipart"
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
const MaxAttachmentSize = 2621440

// An Envelope represents an email that is finalized, parsed, and ready for
// submission.
type Envelope struct {
	From       *Sender
	To         *Recipient
	Subject    string
	Body       string
	Attachment []byte
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
	RetrySec  int
	ExpireSec int
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

	validAttachment := e.Attachment != nil && len(e.Attachment) <= MaxAttachmentSize
	title := e.Subject
	if title == "" {
		title = "(no subject)"
	}
	if e.From.ShowAddress {
		title += " (" + e.From.Address + ")"
	}
	if e.Attachment != nil && !validAttachment {
		title += " (attachment too large)"
	}

	push := &pushover.Message{
		Message:    truncate(e.Body, MaxEmailLength),
		Title:      truncate(title, MaxTitleLength),
		Priority:   e.To.Priority,
		DeviceName: e.To.Device,
		Sound:      e.To.Sound,
		HTML:       true}
	if e.To.RetrySec != 0 {
		push.Retry = time.Duration(e.To.RetrySec) * time.Second
	}
	if e.To.ExpireSec != 0 {
		push.Expire = time.Duration(e.To.ExpireSec) * time.Second
	}
	if validAttachment {
		push.AddAttachment(bytes.NewBuffer(e.Attachment))
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
					q <- makeEnvelope(parsedSndr, parsedRcpt, msg, errl)
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

	user := findStringSubmatch(`^(u\w+)((?:>[\w,]+|#[-\+]?\d|!\w+|@\d+|\$\d+)*)@`, addr)
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

	retry := findStringSubmatch(`@(\d+)`, opts)
	if len(retry) == 2 {
		r.RetrySec, _ = strconv.Atoi(retry[1])
	}

	expire := findStringSubmatch(`\$(\d+)`, opts)
	if len(expire) == 2 {
		r.ExpireSec, _ = strconv.Atoi(expire[1])
	}

	sound := findStringSubmatch(`!(\w+)`, opts)
	if len(sound) == 2 {
		r.Sound = sound[1]
	}

	return
}

// makeEnvelope extracts plaintext versions of the Message's subject and body
// as well as the binary version of the attachment, if any.
func makeEnvelope(sndr *Sender, rcpt *Recipient, m *mail.Message, errl *log.Logger) *Envelope {
	contentType := m.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)

	var body string
	var attachment []byte
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(m.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if strings.HasPrefix(part.Header.Get("Content-Type"), "text/") {
				body = decodeIfEncoded(readAllAsString(part))
			} else if bytes, err := ioutil.ReadAll(part); err == nil {
				switch encoding := part.Header.Get("Content-Transfer-Encoding"); encoding {
				case "base64":
					buf := make([]byte, len(bytes))
					if nbytes, err := base64.StdEncoding.Decode(buf, bytes); err == nil {
						attachment = buf[0:nbytes]
					} else {
						errl.Println("multipart base64 decode failed")
					}
				default:
					errl.Println("unknown multipart encoding:", encoding)
				}
			}
		}
	} else {
		body = decodeIfEncoded(readAllAsString(m.Body))
	}

	return &Envelope{
		From:       sndr,
		To:         rcpt,
		Subject:    decodeIfEncoded(m.Header.Get("Subject")),
		Body:       body,
		Attachment: attachment}
}

func decodeIfEncoded(s string) string {
	if match, _ := regexp.MatchString(`^\s*=\?[^\?]+\?[bBqQ]\?[^\?]+\?=\s*$`, s); match {
		if res, err := new(mime.WordDecoder).Decode(s); err != nil {
			return res
		}
		return s
	}
	return s
}

func readAllAsString(r io.Reader) string {
	bytes, err := ioutil.ReadAll(r)
	if err != nil {
		return ""
	}
	return string(bytes)
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
