package ovirt

import (
	ovirtsdk "github.com/ovirt/go-ovirt"
)

type ovirtClient struct {
	connection *ovirtsdk.Connection
}

func NewOvirtClient(conn *ovirtsdk.Connection) OvirtClient {
	return &ovirtClient{
		connection: conn,
	}
}
