#!/usr/bin/env python3

from email import encoders
from email.mime.multipart import MIMEMultipart
from email.mime.base import MIMEBase
from email.mime.text import MIMEText
from os import environ
from smtplib import SMTP


sender = 'trump@whitehouse.gov'
receiver = f'{environ["PUSHOVER_USER"]}@api.pushover.net'

message = MIMEMultipart()
message['From'] = sender
message['To'] = receiver
message['Subject'] = 'Test email'

message.attach(MIMEText('This email contains an attached image.', 'plain'))

part = MIMEBase('application', 'octet-stream')
FILE = 'BBridge.jpg'
with open(FILE, 'rb') as attachment:
    part.set_payload(attachment.read())
encoders.encode_base64(part)
part.add_header(
    'Content-Disposition',
    f'attachment; filename={FILE}'
)
message.attach(part)

with SMTP('localhost', 2525) as smtp:
    smtp.sendmail(sender, receiver, message.as_string())
