FROM alpine:latest
EXPOSE 2121
COPY sample/conf/settings.toml /etc/ftpserver.conf
COPY ftpserver /bin/ftpserver
ENTRYPOINT [ "/bin/ftpserver" ]
