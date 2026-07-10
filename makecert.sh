#!/bin/sh

# Mock TLS certificates generator

openssl req -nodes -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -sha256 -days 365
