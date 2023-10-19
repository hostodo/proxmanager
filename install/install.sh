#!/bin/bash

NODE_HOSTNAME=$1

adduser proxmanager
cp ./config/proxmanager-py.service /etc/systemd/system/proxmanager-py.service

mkdir /var/log/proxmanager

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

    # 1214 Geneva AT&T Fiber
    allow 69.221.240.6;
    # Odopanel API server
    allow 66.187.7.3;
    deny all;

    location / {
        include proxy_params;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header Host \$http_host;
        proxy_pass http://proxmanager_py;
    }
}
EOL

ln -s /etc/nginx/sites-available/proxmanager.conf /etc/nginx/sites-enabled/proxmanager.conf

certbot --nginx -d proxmanager.$NODE_HOSTNAME