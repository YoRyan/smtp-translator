![SMTP Translator icon](https://raw.githubusercontent.com/wiki/YoRyan/smtp-translator/header_icon.png)

# SMTP Translator

[Pushover](https://pushover.net) is a fantastic service, but email notification via SMTP remains the standard for Unix daemons, Internet of Things, and embedded devices. SMTP Translator bridges the gap by receiving emails via SMTP and converting them into Pushover notifications, providing a simpler and more secure alternative to replicating your Gmail password on all of your systems.

To use SMTP Translator, just set your SMTP forwader to `smtpt.app.youngryan.com` and send an email to `<your user key here>@api.pushover.net`. (Unfortunately, it is not possible to mimic the [newer, shorter](https://blog.pushover.net/posts/new-e-mail-gateway-features) email gateway addresses.) Then, instead of routing the email to Pushover via the conventional email network, SMTP Translator submits it directly to the Pushover API. You can make up any sender addresses you want, since they never touch the public email system.

Please note that with SMTP Translator as your sole smarthost, your system will not be able to send email to non-Pushover destinations.

##### Q: What's the catch?

No catch. I promise that the code on this repository is what I run on my server, and that I do not log messages or metadata. If you like, of course, you are free to get your own app token and host your own instance.

##### Q: Does `smtpt.app.youngryan.com` support encryption?

Yes. To use TLS encryption, make note of the following table:

| Port | Encryption |
| --- | --- |
| 25 | STARTTLS (optional) |
| 465 | TLS-on-connect |
| 587 | STARTTLS (mandatory) |

In theory, I could still read your messages. Email, by its nature, cannot be end-to-end encrypted.

##### Q: Help! My message didn't go through!

Double-check the token in your recipient address - it is easy to confuse an app token for a user or group token. And make sure your message is at most 1024 characters long, per the Pushover [API limit](https://pushover.net/api#limits).

## Examples

### Synology NAS

![Synology configuration screen](https://raw.githubusercontent.com/wiki/YoRyan/smtp-translator/synology_config.jpg)

### exim4 (Debian/Ubuntu)

Run `dpkg-reconfigure exim4-config` and answer the following:

- General type of mail configuration: "mail sent by smarthost; no local mail"
- IP address or host name of the outgoing smarthost: "smtpt.app.youngryan.com::587"

```
$ mailx -s 'Test Email' 'your.user.key.here@api.pushover.net'
Hello, World!
```

### sendmail

```
# cat >>/etc/mail/sendmail.mc; /etc/mail/make
define(`SMART_HOST', `smtpt.app.youngryan.com')dnl
define(`RELAY_MAILER', `esmtp')dnl
define(`RELAY_MAILER_ARGS', `TCP $h 587')dnl
define(`confAUTH_MECHANISMS', `CRAM-MD5')dnl
```

```
$ mailx -s 'Test Email' 'your.user.key.here@api.pushover.net'
Hello, World!
```

## Run your own

```
$ go get -u github.com/YoRyan/smtp-translator
```

At a minimum you need to specify your Pushover app token in the `PUSHOVER_TOKEN` environment variable.

```
$ export PUSHOVER_TOKEN=xxx
$ sudo smtp-translator
```

Optionally, you can specify your own listening address and advertised hostname.

```
$ smtp-translator -addr 127.0.0.1:2525 -hostname My-Host-Not-Root
```

### Enabling TLS

To quickly generate your own cert:

```
$ openssl req -newkey rsa:4096 -nodes -sha512 -x509 -days 3650 -nodes -out mycert.pem -keyout mycert.key
```

There are three possible operating modes depending on whether you want to encrypt the entire session or kickstart unencrypted connections with STARTTLS - and if you use STARTTLS, whether or not to mandate encryption. For a historical discussion of the differing standards, see this [overview by Fastmail](https://www.fastmail.com/help/technical/ssltlsstarttls.html).

| Arguments | Mode |
| --- | --- |
| `-tls-cert mycert.pem -tls-key mycert.key` | Immediate TLS encryption |
| `-tls-cert mycert.pem -tls-key mycert.key -starttls` | Initial connection unencrypted, optional upgrade to TLS |
| `-tls-cert mycert.pem -tls-key mycert.key -starttls-always` | Initial connection unencrypted, mandatory upgrade to TLS |

### Enabling authentication

To password-protect your server, use the `-auth` switch to provide a path to a credentials file, where each line represents an authorized login in the form of `username:password`.

```
$ cat >mycreds.txt <<EOF
ryan:hunter2
trump:letmein
EOF
$ chmod 600 mycreds.txt
$ smtp-translator -addr :2525 -auth mycreds.txt
```

A valid login will then be required to submit any messages. Provide usernames and passwords to your SMTP clients as you would for any SMTP server that requires authentication. If not using TLS, clients must support the CRAM-MD5 authentication method so that they do not reveal passwords in transit.
