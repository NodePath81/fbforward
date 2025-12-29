package main

import (
	"net"
	"strconv"
)

func formatPort(port int) string {
	return strconv.Itoa(port)
}

func netJoin(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
