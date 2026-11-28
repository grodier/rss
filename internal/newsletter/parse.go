// Package newsletter reads received emails and decides what they are: who
// sent them, whether Cloudflare authenticated that sender, which feed they
// belong to and what kind of mail they are. It's pure: nothing here touches
// the database or the network. See "Newsletters" in docs/decisions.md.
package newsletter

import (
	"bytes"
	"encoding/base64"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/textcut"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	// maxHeaderBytes is the most of any header value kept.
	maxHeaderBytes = 4 << 10
	// maxBodyBytes is the most of the body kept.
	maxBodyBytes = 1 << 20
	// maxAuthResults is the most Authentication-Results headers kept: the
	// one that counts is near the top.
	maxAuthResults = 20
	// maxARCSets is the most ARC sets kept (ARC allows i=1 to i=50).
	maxARCSets = 50
	// maxAddresses is the most addresses kept from To or Cc.
	maxAddresses = 50
	// maxDepth is how deep multipart parts are followed.
	maxDepth = 8
)

var wordDecoder = &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}

// Parse reads a raw RFC 5322 message into an rss.Email. Only a message
// whose headers can't be read is an error; a body it can't decode is kept
// as far as it decoded.
func Parse(raw []byte) (rss.Email, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return rss.Email{}, err
	}
	h := msg.Header
	addrs := &mail.AddressParser{WordDecoder: wordDecoder}

	var e rss.Email
	if from, err := addrs.Parse(h.Get("From")); err == nil {
		e.FromName = cut(from.Name)
		e.From = cut(strings.ToLower(from.Address))
	}
	e.To = addressList(addrs, h.Get("To"))
	e.Cc = addressList(addrs, h.Get("Cc"))
	if rt, err := addrs.Parse(h.Get("Reply-To")); err == nil {
		e.ReplyTo = cut(strings.ToLower(rt.Address))
	}
	e.Subject = cut(decodeWords(h.Get("Subject")))
	e.MessageID = cut(strings.TrimSpace(h.Get("Message-ID")))
	if d, err := mail.ParseDate(h.Get("Date")); err == nil {
		e.Date = d
	}
	e.ListID = cut(listID(h.Get("List-Id")))
	e.ListUnsubscribe = cut(strings.TrimSpace(h.Get("List-Unsubscribe")))
	e.ListUnsubscribePost = cut(strings.TrimSpace(h.Get("List-Unsubscribe-Post")))

	for i, v := range h["Authentication-Results"] {
		if i == maxAuthResults {
			break
		}
		e.AuthenticationResults = append(e.AuthenticationResults, cut(v))
	}
	e.ARC = arcSets(h["Arc-Seal"], h["Arc-Authentication-Results"])

	var p bodyParts
	p.walk(h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), msg.Body, 0)
	text := p.text
	switch {
	case p.hasHTML:
		e.HTML = p.html
		if !p.hasText {
			text = htmlText(p.html)
		}
	case p.hasText:
		e.HTML = textHTML(p.text)
	}
	e.HTML = textcut.Bytes(e.HTML, maxBodyBytes)
	e.ForwardedFrom = cut(forwardedSender(text))
	return e, nil
}

// cut makes a header value safe to keep: valid UTF-8, no NUL bytes, at most
// maxHeaderBytes.
func cut(s string) string {
	return textcut.Bytes(s, maxHeaderBytes)
}

// decodeWords decodes a header's RFC 2047 encoded words, keeping the value
// as it is when they can't be decoded.
func decodeWords(s string) string {
	d, err := wordDecoder.DecodeHeader(s)
	if err != nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(d)
}

// addressList returns the lowercased addresses of a To or Cc header, none
// when it can't be read.
func addressList(p *mail.AddressParser, v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	list, err := p.ParseList(v)
	if err != nil {
		return nil
	}
	var out []string
	for i, a := range list {
		if i == maxAddresses {
			break
		}
		out = append(out, cut(strings.ToLower(a.Address)))
	}
	return out
}

// listID returns a List-Id header's identifier: what's inside its last
// angle brackets ("Example News <news.example.com>"), lowercased.
func listID(v string) string {
	open, end := strings.LastIndex(v, "<"), strings.LastIndex(v, ">")
	if open < 0 || end < open {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(v[open+1 : end]))
}

// arcSets pairs the ARC-Seal and ARC-Authentication-Results headers by
// instance. A set without a seal has no sealer, so it's never trusted.
func arcSets(seals, results []string) []rss.ARCSet {
	byInstance := map[int]*rss.ARCSet{}
	set := func(i int) *rss.ARCSet {
		if byInstance[i] == nil {
			byInstance[i] = &rss.ARCSet{Instance: i}
		}
		return byInstance[i]
	}
	for _, v := range seals {
		tags := tagList(v)
		i, err := strconv.Atoi(tags["i"])
		if err != nil || i < 1 || i > maxARCSets {
			continue
		}
		set(i).Sealer = cut(strings.ToLower(tags["d"]))
	}
	for _, v := range results {
		head, rest, ok := strings.Cut(v, ";")
		k, n, _ := strings.Cut(strings.TrimSpace(head), "=")
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if !ok || strings.TrimSpace(k) != "i" || err != nil || i < 1 || i > maxARCSets {
			continue
		}
		set(i).Results = cut(strings.TrimSpace(rest))
	}
	out := make([]rss.ARCSet, 0, len(byInstance))
	for _, s := range byInstance {
		out = append(out, *s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Instance < out[b].Instance })
	return out
}

// tagList reads a DKIM-style tag list, "i=1; d=example.com; ...".
func tagList(v string) map[string]string {
	tags := map[string]string{}
	for _, t := range strings.Split(v, ";") {
		k, val, ok := strings.Cut(t, "=")
		if ok {
			tags[strings.TrimSpace(k)] = strings.Join(strings.Fields(val), "")
		}
	}
	return tags
}

// bodyParts collects the first HTML part and the first text part of a
// message, skipping attachments.
type bodyParts struct {
	html, text       string
	hasHTML, hasText bool
}

func (p *bodyParts) walk(contentType, encoding string, r io.Reader, depth int) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// RFC 2045's default, also for a header we can't read.
		media, params = "text/plain", nil
	}
	if strings.HasPrefix(media, "multipart/") {
		if depth >= maxDepth || params["boundary"] == "" {
			return
		}
		mr := multipart.NewReader(r, params["boundary"])
		for {
			part, err := mr.NextRawPart()
			if err != nil {
				return
			}
			if attachment(part.Header.Get("Content-Disposition")) {
				continue
			}
			p.walk(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, depth+1)
		}
	}
	switch {
	case media == "text/html" && !p.hasHTML:
		p.html, p.hasHTML = decodeText(r, encoding, params["charset"]), true
	case media == "text/plain" && !p.hasText:
		p.text, p.hasText = decodeText(r, encoding, params["charset"]), true
	}
}

// attachment reports whether a part's Content-Disposition makes it an
// attachment.
func attachment(disposition string) bool {
	d, _, err := mime.ParseMediaType(disposition)
	return err == nil && d == "attachment"
}

// decodeText decodes a text part's transfer encoding and charset into
// UTF-8, keeping what decoded when the part is broken. An unknown charset
// is read as it is (textcut.Bytes replaces what isn't UTF-8).
func decodeText(r io.Reader, encoding, cs string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	if cs = strings.ToLower(strings.TrimSpace(cs)); cs != "" && cs != "utf-8" && cs != "us-ascii" {
		if cr, err := charset.NewReaderLabel(cs, r); err == nil {
			r = cr
		}
	}
	b, _ := io.ReadAll(io.LimitReader(r, maxBodyBytes))
	return textcut.Bytes(string(b), maxBodyBytes)
}

var blankLines = regexp.MustCompile(`\n[ \t]*\n`)

// textHTML wraps a text body in paragraphs, a blank line apart, its line
// breaks kept.
func textHTML(text string) string {
	var b strings.Builder
	for _, para := range blankLines.Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(para), "\n", "<br>"))
		b.WriteString("</p>\n")
	}
	return b.String()
}

// blockTags end a line in htmlText.
var blockTags = map[string]bool{"br": true, "p": true, "div": true, "tr": true, "li": true, "blockquote": true, "table": true, "h1": true, "h2": true, "h3": true, "h4": true, "hr": true}

// htmlText returns an HTML body's text, a line per block, for finding a
// forwarded block and bare links in it.
func htmlText(s string) string {
	var b strings.Builder
	z := xhtml.NewTokenizer(strings.NewReader(s))
	skip := 0 // inside script or style
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return b.String()
		case xhtml.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case xhtml.StartTagToken, xhtml.EndTagToken, xhtml.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data == "script" || tok.Data == "style" {
				if tok.Type == xhtml.StartTagToken {
					skip++
				} else if tok.Type == xhtml.EndTagToken && skip > 0 {
					skip--
				}
			}
			if blockTags[tok.Data] {
				b.WriteByte('\n')
			}
		}
	}
}

var bareAddress = regexp.MustCompile(`[^\s<>"'(),;:]+@[^\s<>"'(),;:]+\.[^\s<>"'(),;:]+`)

// forwardedSender returns the sender's address in a forwarded block in a
// body's text: after Gmail's "---------- Forwarded message ---------" or
// Apple Mail's "Begin forwarded message:", or Outlook's "From:" line
// followed by "Sent:". #834 adds more formats.
func forwardedSender(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := range lines {
		line := trimQuote(lines[i])
		switch {
		case strings.HasPrefix(line, "-") && strings.Contains(line, "Forwarded message"),
			line == "Begin forwarded message:":
			for _, next := range lines[i+1 : min(i+8, len(lines))] {
				if v, ok := strings.CutPrefix(trimQuote(next), "From:"); ok {
					return address(v)
				}
			}
		case strings.HasPrefix(line, "From:"):
			for _, next := range lines[i+1 : min(i+3, len(lines))] {
				if strings.HasPrefix(trimQuote(next), "Sent:") {
					return address(strings.TrimPrefix(line, "From:"))
				}
			}
		}
	}
	return ""
}

// trimQuote trims a line's spaces and quoting ("> ").
func trimQuote(s string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "> "))
}

// address returns the lowercased address in a forwarded block's From line,
// "Example News <news@example.com>" or a bare address.
func address(v string) string {
	if a, err := mail.ParseAddress(strings.TrimSpace(v)); err == nil {
		return strings.ToLower(a.Address)
	}
	return strings.ToLower(bareAddress.FindString(v))
}
