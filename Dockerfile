FROM ubuntu:latest
LABEL authors="guan"

COPY test.txt /usr/local/test.txt
CMD ["bash", "-c", "tail -f /dev/null"]