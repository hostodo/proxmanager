#!/bin/bash

echo "Starting gunicorn $ENV";
if [ "$ENV" = "dev" ]; then
  gunicorn --bind=:6999 --workers=3 --timeout=600 --access-logfile=- --error-logfile=- --reload app:app
elif [ "$ENV" = "local" ]; then
  gunicorn --bind=:6999 --workers=3 --timeout=600 --access-logfile=- --error-logfile=- --reload app:app
else
  gunicorn --bind=:6999 --workers=6 --timeout=600 --access-logfile=- --error-logfile=- app:app
fi

while [ true ]; do
  sleep 5;
done;