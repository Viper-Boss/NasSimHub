// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

func (d *Dispatcher) connect(ctx context.Context, host, port string, encrypted bool) (net.Conn, error) {
	if host == "" || strings.ContainsAny(host, " \r\n\t/") {
		return nil, ErrDenied
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, ErrDelivery
	}
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if encrypted {
		secure := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: d.tlsRoots})
		if err = secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, ErrDelivery
		}
		return secure, nil
	}
	return conn, nil
}
func (d *Dispatcher) sendEmail(ctx context.Context, ch Channel, m Rendered) error {
	p := ch.Params
	port, err := integer(p, "port", 587)
	if err != nil || port < 1 || port > 65535 {
		return ErrDenied
	}
	if strings.ContainsAny(p["from"]+p["to"], "\r\n") {
		return ErrDenied
	}
	from, err := mail.ParseAddress(p["from"])
	if err != nil {
		return ErrDenied
	}
	to, err := mail.ParseAddressList(p["to"])
	if err != nil || len(to) == 0 || len(to) > 20 {
		return ErrDenied
	}
	mode := p["tls"]
	if mode == "" {
		mode = "starttls"
	}
	if mode != "starttls" && mode != "implicit" {
		return ErrDenied
	}
	conn, err := d.connect(ctx, p["host"], fmt.Sprint(port), mode == "implicit")
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, p["host"])
	if err != nil {
		return ErrDelivery
	}
	defer client.Close()
	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return ErrDelivery
		}
		if err = client.StartTLS(&tls.Config{ServerName: p["host"], MinVersion: tls.VersionTLS12, RootCAs: d.tlsRoots}); err != nil {
			return ErrDelivery
		}
	}
	if p["username"] != "" {
		password, err := d.secret(ch, "password")
		if err != nil {
			return err
		}
		if err = client.Auth(smtp.PlainAuth("", p["username"], password, p["host"])); err != nil {
			return ErrDelivery
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return ErrDelivery
	}
	names := []string{}
	for _, recipient := range to {
		if err = client.Rcpt(recipient.Address); err != nil {
			return ErrDelivery
		}
		names = append(names, recipient.String())
	}
	writer, err := client.Data()
	if err != nil {
		return ErrDelivery
	}
	title := strings.NewReplacer("\r", " ", "\n", " ").Replace(m.Title)
	header := "From: " + from.String() + "\r\nTo: " + strings.Join(names, ", ") + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", title) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	if _, err = io.WriteString(writer, header); err != nil {
		return ErrDelivery
	}
	body := base64.StdEncoding.EncodeToString([]byte(m.Body))
	for len(body) > 0 {
		n := min(76, len(body))
		if _, err = io.WriteString(writer, body[:n]+"\r\n"); err != nil {
			return ErrDelivery
		}
		body = body[n:]
	}
	if err = writer.Close(); err != nil {
		return ErrDelivery
	}
	if err = client.Quit(); err != nil {
		return ErrDelivery
	}
	return nil
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return ErrDelivery
		}
		if n == 0 {
			return ErrDelivery
		}
		b = b[n:]
	}
	return nil
}
func mqttString(s string) []byte {
	out := make([]byte, 2+len(s))
	binary.BigEndian.PutUint16(out, uint16(len(s)))
	copy(out[2:], s)
	return out
}
func mqttPacket(header byte, body []byte) []byte {
	out := []byte{header}
	n := len(body)
	for {
		digit := byte(n % 128)
		n /= 128
		if n > 0 {
			digit |= 128
		}
		out = append(out, digit)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}
func mqttRead(r io.Reader) (byte, []byte, error) {
	var one [1]byte
	if _, err := io.ReadFull(r, one[:]); err != nil {
		return 0, nil, ErrDelivery
	}
	header := one[0]
	length, multiplier := 0, 1
	for i := 0; i < 4; i++ {
		if _, err := io.ReadFull(r, one[:]); err != nil {
			return 0, nil, ErrDelivery
		}
		length += int(one[0]&127) * multiplier
		if length > 256 {
			return 0, nil, ErrDelivery
		}
		if one[0]&128 == 0 {
			body := make([]byte, length)
			_, err := io.ReadFull(r, body)
			return header, body, err
		}
		multiplier *= 128
	}
	return 0, nil, ErrDelivery
}
func (d *Dispatcher) sendSocket(ctx context.Context, ch Channel, text, requestID string) error {
	p := ch.Params
	port, err := integer(p, "port", 0)
	if err != nil || port < 1 || port > 65535 || p["host"] == "" || len(text) > 65536 {
		return ErrDenied
	}
	if p["tls"] != "" && p["tls"] != "true" && p["tls"] != "false" {
		return ErrDenied
	}
	if p["protocol"] == "udp" {
		if p["tls"] == "true" || len(text) > 1400 {
			return ErrDenied
		}
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "udp", net.JoinHostPort(p["host"], fmt.Sprint(port)))
		if err != nil {
			return ErrDelivery
		}
		defer conn.Close()
		deadline := time.Now().Add(10 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetWriteDeadline(deadline)
		return writeAll(conn, []byte(text))
	}
	if p["protocol"] != "tcp" && p["protocol"] != "mqtt" {
		return ErrDenied
	}
	conn, err := d.connect(ctx, p["host"], fmt.Sprint(port), p["tls"] == "true")
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if p["protocol"] == "tcp" {
		return writeAll(conn, []byte(text))
	}
	if p["topic"] == "" || strings.ContainsAny(p["topic"], "+#\x00") || len(p["topic"]) > 512 {
		return ErrDenied
	}
	flags := byte(2)
	id := p["client_id"]
	if id == "" {
		id = "nsh_" + requestID
	}
	if len(id) > 64 {
		return ErrDenied
	}
	payload := mqttString(id)
	if p["username"] != "" {
		flags |= 128
		payload = append(payload, mqttString(p["username"])...)
	}
	s := ch.Secrets["password"]
	if s.Env != "" || len(s.Ciphertext) > 0 {
		if p["username"] == "" {
			return ErrDenied
		}
		password, err := d.secret(ch, "password")
		if err != nil {
			return err
		}
		flags |= 64
		payload = append(payload, mqttString(password)...)
	}
	body := append([]byte{0, 4, 'M', 'Q', 'T', 'T', 4, flags, 0, 30}, payload...)
	if err = writeAll(conn, mqttPacket(0x10, body)); err != nil {
		return err
	}
	header, ack, err := mqttRead(conn)
	if err != nil || header != 0x20 || len(ack) != 2 || ack[1] != 0 {
		return ErrDelivery
	}
	publish := append(mqttString(p["topic"]), 0, 1)
	publish = append(publish, []byte(text)...)
	if err = writeAll(conn, mqttPacket(0x32, publish)); err != nil {
		return err
	}
	header, ack, err = mqttRead(conn)
	if err != nil || header != 0x40 || len(ack) != 2 || ack[0] != 0 || ack[1] != 1 {
		return ErrDelivery
	}
	return writeAll(conn, []byte{0xe0, 0})
}
