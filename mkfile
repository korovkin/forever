travis:
	go build -o forever cmd/*.go

build: thrift
	go build -o forever cmd/*.go
