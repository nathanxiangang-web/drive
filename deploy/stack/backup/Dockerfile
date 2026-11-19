FROM docker.io/library/postgres@sha256:68e33adb18d9962952f00d5e3b8644305e10e375f98dd652bf6ee6a1836314ba
RUN apk add --no-cache python3 tzdata ca-certificates tar gzip
COPY 115driver /usr/local/bin/115driver
COPY LICENSE.115driver /usr/share/licenses/115driver/LICENSE
COPY backup.py /usr/local/lib/cloud-drive-backup.py
ENTRYPOINT ["python3", "-u", "/usr/local/lib/cloud-drive-backup.py"]
CMD []
