travis:
	go build -o forever cmd/*.go

build: 
	go build -o forever cmd/*.go

test: build
	cat example/commands.txt  | ./forever
