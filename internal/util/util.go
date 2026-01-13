package util

import (
	"net"
	"strconv"
)

func FormatPort(port int) string {
	return strconv.Itoa(port)
}

func NetJoin(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
