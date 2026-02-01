// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"net"
	"strconv"

	"github.com/efficientgo/core/errors"
)

type NetDialer struct{}

func (_ NetDialer) Dial(t Target) (Client, error) {
	targetString := t.Host + ":" + strconv.Itoa(t.Port)
	conn, err := net.Dial("tcp", targetString)

	if err != nil {
		return nil, errors.Wrap(
			err,
			"Failed to connect to USB/IP target at "+targetString,
		)
	}

	usbipConn := &Connection{
		Target:     t,
		connection: conn.(*net.TCPConn),
	}
	return usbipConn, nil
}

func (c *Connection) Close() {
	if c.connection != nil {
		_ = c.connection.Close()
		c.connection = nil
	}
}
