# SMTP Translator

[Pushover](https://pushover.net) is a useful service, but email notification via SMTP remains the standard for Unix daemons, Internet of Things, and embedded devices. SMTP Translator bridges the gap by receiving emails via SMTP and converting them into Pushover notifications, providing a simpler and more secure alternative to replicating your Gmail password on all of your systems. All emails reach your feed regardless of who sent them or to whom they were addressed - in fact, you can make up your own imaginary email addresses, since they are isolated from the global email system. You can run SMTP Translator on your personal intranet or, after first enabling authentication, on a public server on the Internet.

(Note that TLS has not yet been implemented, so all traffic is currently transmitted in plaintext.)

## Authentication

To password-protect your server, use the `-auth` switch to provide a path to a credentials file, where each line represents an authorized login in the form of `username:password`:

```
ryan:hunter2
trump:letmein
```

A valid login will then be required to submit any messages. Provide username and passwords to your SMTP clients as you would for any SMTP server that requires authentication. Clients must support the CRAM-MD5 authentication method, which does not reveal passwords in transit.
