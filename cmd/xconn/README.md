# xconn

A CLI tool for starting the XConn server. It initializes the basic configuration and starts the XConn server using that
configuration.

## CLI

The command-line interface overview:

```shell
muzzammil@OfficePC:~$ xconn
usage: xconn [<flags>] <command> [<args> ...]

XConn


Flags:
      --[no-]help     Show context-sensitive help (also try --help-long and
                      --help-man).
  -v, --[no-]version  Show application version.
  -c, --config="/home/muzzammil/.xconn"  
                      Set config directory

Commands:
help [<command>...]
    Show help.

init
    Initialize sample router config.

start
    Start the router.

```
## Installation

```shell
sudo snap install xconn
```

## Building

```shell
git clone git@github.com:xconnio/xconn-go.git
cd xconn-go
make build
```