FROM alpine:3.13

RUN apk --no-cache add ca-certificates
RUN mkdir -p /var/runtime
COPY bootstrap /var/runtime/bootstrap
WORKDIR /var/runtime
CMD ["/var/runtime/bootstrap"]
