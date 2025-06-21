run:
	go build -o bin/pion-client
	./bin/pion-client

peer:
	go build -o bin/pion-client
	./bin/pion-client -peer=host:port