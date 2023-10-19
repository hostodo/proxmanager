#!/bin/bash

adduser proxmanager
cp ./config/proxmanager-py.service /etc/systemd/system/proxmanager-py.service
