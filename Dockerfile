FROM alpine:latest
COPY ftpserver /bin/ftpserver
ENTRYPOINT ["/bin/ftpserver"]
