# SMTP Translator

[Pushover](https://pushover.net) is a useful service, but email notification via SMTP remains the standard for Unix daemons, Internet of Things, and embedded devices. SMTP Translator bridges the gap by receiving emails via SMTP and converting them into Pushover notifications, providing a simpler and more secure alternative to replicating your Gmail password on all of your systems. All emails reach your feed regardless of who sent them or to whom they were addressed - in fact, you can make up your own imaginary email addresses, since they are isolated from the global email system. You can run SMTP Translator on your personal intranet or, after first enabling TLS and authentication, on a public server on the Internet.

## Usage

At a minimum you need to specify your Pushover tokens in the `PUSHOVER_TOKEN` and `PUSHOVER_USER` environment variables.

```
$ export PUSHOVER_TOKEN=xxx
$ export PUSHOVER_USER=xxx
$ sudo smtp-translator
```

Optionally, you can specify your own listening address and advertised hostname.

```
$ smtp-translator -listen 127.0.0.1:2525 -hostname My-Host-Not-Root
```

### Enabling TLS

To quickly generate your own cert:

```
openssl req -newkey rsa:4096 -nodes -sha512 -x509 -days 3650 -nodes -out mycert.pem -keyout mycert.key
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
$ smtp-translator -listen :2525 -auth mycreds.txt
```

A valid login will then be required to submit any messages. Provide usernames and passwords to your SMTP clients as you would for any SMTP server that requires authentication. If not using TLS, clients must support the CRAM-MD5 authentication method so that they do not reveal passwords in transit.
