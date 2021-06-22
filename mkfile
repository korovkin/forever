travis:
	go build -o forever cmd/*.go

build: 
	go build -o forever cmd/*.go

install:
	go install -v cmd/*.go

test: build
	cat example/commands.txt  | ./forever
