FROM scratch
ADD kafka-offset-exporter /
EXPOSE 9000
ENTRYPOINT ["/kafka-offset-exporter"]
CMD ["-brokers", "127.0.0.1:9092", "-topics", "^[^_].*", "-groups", "."]
