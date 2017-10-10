FROM alpine:latest
EXPOSE 2121
COPY ftpserver /bin/ftpserver
#ENTRYPOINT [ "/bin/ftpserver" ]
