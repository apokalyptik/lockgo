.PHONY: all clean

all: lockgo

lockgo: main.go
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $@ $<

clean:
	rm -f lockgo
