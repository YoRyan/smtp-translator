#!/usr/bin/env python3

from os import environ
from smtplib import SMTP


sender = 'lance@bikeleague.org'
receivers = [f'{environ["PUSHOVER_USER"]}!bike@api.pushover.net']

message = """Subject: Test email

The wheels on the bike go round and round!
"""

with SMTP('localhost', 2525) as smtp:
    recv_head = ', '.join(f'<{r}>' for r in receivers)
    smtp.sendmail(sender, receivers, f'From: <{sender}>\nTo: {recv_head}\n{message}')
