package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/storage"
)

// Gmail's X-GM-LABELS extension is not exposed by go-imap v2. This small,
// bounded command adapter uses an independent authenticated TLS connection.
type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

var validityPattern = regexp.MustCompile(`(?i)\[UIDVALIDITY ([0-9]+)\]`)
var gmailIDPattern = regexp.MustCompile(`(?i)X-GM-MSGID ([0-9]+)`)

func GmailArchive(ctx context.Context, a storage.Account, credentials auth.Credentials, d ContextDialer, cfg *tls.Config, source storage.Folder, uid uint32, allMail, messageID string) (destinationUID, validity uint32, result error) {
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(a.IMAPHost, strconv.Itoa(a.IMAPPort)))
	if err != nil {
		return 0, 0, fmt.Errorf("gmail archive dial: %w", err)
	}
	raw := conn
	stop := context.AfterFunc(ctx, func() {
		if err := raw.Close(); err != nil {
			return
		}
	})
	defer func() {
		stop()
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, fmt.Errorf("gmail archive close: %w", err))
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, 0, err
	}
	tlsConfig := &tls.Config{ServerName: a.IMAPHost, MinVersion: tls.VersionTLS12}
	if cfg != nil {
		tlsConfig = cfg.Clone()
		tlsConfig.ServerName = a.IMAPHost
	}
	if a.IMAPTLS == "implicit" {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return 0, 0, fmt.Errorf("gmail archive TLS: %w", err)
		}
		conn = secure
	}
	reader := bufio.NewReaderSize(conn, 64*1024)
	greeting, err := readGmailLine(reader)
	if err != nil || !strings.HasPrefix(greeting, "* OK") {
		return 0, 0, fmt.Errorf("gmail archive: invalid greeting")
	}
	sequence := 0
	command := func(text string) ([]string, error) {
		sequence++
		tag := "MM" + strconv.Itoa(sequence)
		if _, err := io.WriteString(conn, tag+" "+text+"\r\n"); err != nil {
			return nil, fmt.Errorf("gmail archive command write: %w", err)
		}
		lines := make([]string, 0)
		total := 0
		for i := 0; i < 1000; i++ {
			line, err := readGmailLine(reader)
			if err != nil {
				return nil, err
			}
			total += len(line)
			if total > 256*1024 {
				return nil, fmt.Errorf("gmail archive: response too large")
			}
			if strings.HasPrefix(line, "+ ") {
				if _, err := io.WriteString(conn, "\r\n"); err != nil {
					return nil, err
				}
				continue
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					return nil, fmt.Errorf("gmail archive: command rejected")
				}
				return lines, nil
			}
			lines = append(lines, line)
		}
		return nil, fmt.Errorf("gmail archive: too many responses")
	}
	if a.IMAPTLS == "starttls" {
		if _, err := command("STARTTLS"); err != nil {
			return 0, 0, err
		}
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return 0, 0, err
		}
		conn = secure
		reader = bufio.NewReaderSize(conn, 64*1024)
	} else if a.IMAPTLS != "implicit" {
		return 0, 0, fmt.Errorf("gmail archive: TLS required")
	}
	quote := func(value string) (string, error) {
		if strings.ContainsAny(value, "\r\n\x00") {
			return "", fmt.Errorf("gmail archive: invalid quoted value")
		}
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`, nil
	}
	if a.AuthType == "oauth2" {
		sasl := base64.StdEncoding.EncodeToString([]byte("user=" + a.Email + "\x01auth=Bearer " + credentials.AccessToken + "\x01\x01"))
		if _, err := command("AUTHENTICATE XOAUTH2 " + sasl); err != nil {
			return 0, 0, err
		}
	} else {
		user, err := quote(a.Email)
		if err != nil {
			return 0, 0, err
		}
		password, err := quote(credentials.Password)
		if err != nil {
			return 0, 0, err
		}
		if _, err := command("LOGIN " + user + " " + password); err != nil {
			return 0, 0, err
		}
	}
	selectFolder := func(name string) (uint32, error) {
		encoded, err := quote(encodeMailbox(name))
		if err != nil {
			return 0, err
		}
		lines, err := command("SELECT " + encoded)
		if err != nil {
			return 0, err
		}
		for _, line := range lines {
			match := validityPattern.FindStringSubmatch(line)
			if len(match) == 2 {
				number, err := strconv.ParseUint(match[1], 10, 32)
				if err != nil {
					return 0, err
				}
				return uint32(number), nil
			}
		}
		return 0, fmt.Errorf("gmail archive: missing UIDVALIDITY")
	}
	sourceValidity, err := selectFolder(source.Name)
	if err != nil {
		return 0, 0, err
	}
	if sourceValidity != source.UIDValidity {
		return 0, 0, fmt.Errorf("gmail archive: source UIDVALIDITY changed")
	}
	lines, err := command(fmt.Sprintf("UID FETCH %d (X-GM-MSGID)", uid))
	if err != nil {
		return 0, 0, err
	}
	gmailID := ""
	for _, line := range lines {
		if match := gmailIDPattern.FindStringSubmatch(line); len(match) == 2 {
			gmailID = match[1]
		}
	}
	validity, err = selectFolder(allMail)
	if err != nil {
		return 0, 0, err
	}
	search := ""
	if gmailID != "" {
		search = "UID SEARCH X-GM-MSGID " + gmailID
	} else if messageID != "" {
		value, err := quote(strings.Trim(messageID, "<>"))
		if err != nil {
			return 0, 0, err
		}
		search = "UID SEARCH HEADER Message-ID " + value
	} else {
		return 0, 0, fmt.Errorf("gmail archive: source message not found")
	}
	lines, err = command(search)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "*" && fields[1] == "SEARCH" {
			number, err := strconv.ParseUint(fields[2], 10, 32)
			if err != nil {
				return 0, 0, err
			}
			destinationUID = uint32(number)
		}
	}
	if destinationUID == 0 {
		return 0, 0, fmt.Errorf("gmail archive: All Mail UID is ambiguous")
	}
	if gmailID == "" {
		return destinationUID, validity, nil
	} // Already removed from Inbox on a previous attempt.
	if selected, err := selectFolder(source.Name); err != nil {
		return 0, 0, err
	} else if selected != source.UIDValidity {
		return 0, 0, fmt.Errorf("gmail archive: source UIDVALIDITY changed")
	}
	if _, err := command(fmt.Sprintf("UID STORE %d -X-GM-LABELS.SILENT (\\Inbox)", uid)); err != nil {
		return 0, 0, err
	}
	return destinationUID, validity, nil
}
func readGmailLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return "", fmt.Errorf("gmail archive response: %w", err)
	}
	return strings.TrimRight(string(line), "\r\n"), nil
}
func encodeMailbox(name string) string {
	var result strings.Builder
	var nonASCII []rune
	flush := func() {
		if len(nonASCII) == 0 {
			return
		}
		units := utf16.Encode(nonASCII)
		data := make([]byte, len(units)*2)
		for i, unit := range units {
			binary.BigEndian.PutUint16(data[i*2:], unit)
		}
		result.WriteByte('&')
		result.WriteString(strings.ReplaceAll(strings.TrimRight(base64.StdEncoding.EncodeToString(data), "="), "/", ","))
		result.WriteByte('-')
		nonASCII = nil
	}
	for _, r := range name {
		if r >= 0x20 && r <= 0x7e {
			flush()
			if r == '&' {
				result.WriteString("&-")
			} else {
				result.WriteRune(r)
			}
		} else {
			nonASCII = append(nonASCII, r)
		}
	}
	flush()
	return result.String()
}
