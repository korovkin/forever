GO_BUILD_OPTIONS=-ldflags "-X forever.VERSION_DATE=`date '+%Y-%m-%d_%I:%M:%S%p'` -X forever.VERSION_GIT_HASH=`git rev-parse HEAD`" 

travis:
	go build ${GO_BUILD_OPTIONS} -o forever cmd/*.go

build: 
	go build ${GO_BUILD_OPTIONS} -o forever cmd/*.go

install:
	go install -v cmd/*.go

test: build
	cat example/commands.txt  | ./forever
version:
	python ./scripts/bump_version.py
	git commit --message="version `cat _version.txt`" version.go


