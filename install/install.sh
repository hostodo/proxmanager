#!/bin/bash

adduser proxmanager
cp ./config/proxmanager-py.service /etc/systemd/system/proxmanager-py.service

systemctl start proxmanager
systemctl enable proxmanager
