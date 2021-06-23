# forever

Run a bunch of executables (bash cmd lines) forever

## build:
```
go build -o forever cmd/*.go
```

or just:

```
make travis
```

## Demos:

Run a bunch of processes forever:

```

cat example/commands.txt

date;       sleep 2;        #FOREVER:{"name":"date"}
ls;         sleep 4;        #FOREVER:{"name":"ls"}
date;       sleep 6;        #FOREVER:{"name":"date2"}
ifconfig;   sleep 60;       #FOREVER:{}
ping -c 10 www.google.com ; #FOREVER:{}


cat example/commands.txt  | ./forever

... 

2021/06/23 01:11:28.086244 forever.go:395: concurrency: 100
2021/06/23 01:11:28.086390 forever.go:400: logging to: _log.log *
2021/06/23 01:11:28.086510 forever.go:253: reading from stdin...

[4          i:0004-0000: 01:11:28     0ms I] iter: 0 cmdNum: 4 cmd:  ping -c 10 www.google.com ; #FOREVER:{}
[ls         i:0001-0000: 01:11:28     0ms I] iter: 0 cmdNum: 1 cmd:  ls;         sleep 4;        #FOREVER:{"name":"ls"}
[3          i:0003-0000: 01:11:28     0ms I] iter: 0 cmdNum: 3 cmd:  ifconfig;   sleep 60;       #FOREVER:{}
[date       i:0000-0000: 01:11:28     1ms I] iter: 0 cmdNum: 0 cmd:  date;       sleep 2;        #FOREVER:{"name":"date"}
[date2      i:0002-0000: 01:11:28     1ms I] iter: 0 cmdNum: 2 cmd:  date;       sleep 6;        #FOREVER:{"name":"date2"}

```
