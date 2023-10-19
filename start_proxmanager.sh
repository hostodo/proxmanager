#!/bin/bash
set -a # automatically export all variables
source .env
set +a

echo "Starting gunicorn $ENV";
if [ "$ENV" = "dev" ]; then
  gunicorn --bind=:6999 --workers=3 --timeout=600 --access-logfile=- --error-logfile=- --reload app:app
elif [ "$ENV" = "local" ]; then
  gunicorn --bind=:6999 --workers=3 --timeout=600 --access-logfile=- --error-logfile=- --reload app:app
else
  gunicorn --bind unix:///var/run/proxmanager.sock  --workers=6 --timeout=600 --access-logfile=- --error-logfile=-  --workers 3 -m 007 app:app
fi

while [ true ]; do
  sleep 5;
done;