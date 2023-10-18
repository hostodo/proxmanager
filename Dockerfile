FROM python:3.8
ENV PYTHONUNBUFFERED 1
RUN mkdir /data
WORKDIR /data
ADD requirements.txt /data/
RUN apt-get update -y
RUN apt-get install -y cron
RUN pip3 install -r requirements.txt
ADD . /data/
CMD chmod +x /data/start_proxmanager.sh
CMD /data/start_proxmanager.sh