package cast

import "testing"

func TestEstablishedOn(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1BBC 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0000000000000000 100 0 0 10 0
   1: 2F40640A:1BBC 0A40640A:D2F0 01 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 20 4 30 10 -1
`
	if !establishedOn(table, ":1BBC") {
		t.Fatal("established connection on 7100 not found")
	}
	if establishedOn(table, ":1BBD") {
		t.Fatal("found a connection on the wrong port")
	}
	listenOnly := `  sl  local_address rem_address   st
   0: 00000000:1BBC 00000000:0000 0A 00000000:00000000
`
	if establishedOn(listenOnly, ":1BBC") {
		t.Fatal("a listening socket is not a client")
	}
}
