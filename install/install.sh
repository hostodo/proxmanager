#!/bin/bash

NODE_HOSTNAME=$1

adduser proxmanager
cp ./config/proxmanager-py.service /etc/systemd/system/proxmanager-py.service

systemctl start proxmanager-py
systemctl enable proxmanager-py

apt install nginx certbot python3-certbot-nginx

cat >/etc/nginx/sites-available/proxmanager.conf <<EOL
upstream proxmanager_py {
    server unix:/var/run/proxmanager.sock fail_timeout=0;
}
server {
    listen 80;
    server_name proxmanager.$NODE_HOSTNAME;

    location / {
        include proxy_params;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header Host \$http_host;
        proxy_pass http://proxmanager_py;
    }
}
EOL

ln -s /etc/nginx/sites-available/proxmanager.conf /etc/nginx/sites-enabled/proxmanager.conf
