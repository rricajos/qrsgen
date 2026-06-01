package wameow

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func parseJID(s string) (types.JID, error) {
	jid, err := types.ParseJID(s)
	if err != nil {
		return types.JID{}, fmt.Errorf("parse jid %q: %w", s, err)
	}
	return jid, nil
}

func simpleTextMessage(text string) *waE2E.Message {
	return &waE2E.Message{
		Conversation: proto.String(text),
	}
}

// replyTextMessage construye un ExtendedTextMessage con ContextInfo
// poblado para que el cliente WA receptor renderice el mensaje como
// reply nativo (quoted preview + tap-to-scroll al original).
//
// quotedWAID es el ID del msg original. quotedSenderJID es el JID del
// autor (necesario en grupos; en 1:1 puede ir vacío). quotedText es
// el texto del msg citado (preview). v0.44.0.
func replyTextMessage(text, quotedWAID, quotedSenderJID, quotedText string) *waE2E.Message {
	ci := &waE2E.ContextInfo{
		StanzaID: proto.String(quotedWAID),
		QuotedMessage: &waE2E.Message{
			Conversation: proto.String(quotedText),
		},
	}
	if quotedSenderJID != "" {
		ci.Participant = proto.String(quotedSenderJID)
	}
	return &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(text),
			ContextInfo: ci,
		},
	}
}

// buildMediaMessageWithReply es buildMediaMessage + populates el
// ContextInfo del media correspondiente con quote info. Si los
// args de reply son vacíos, devuelve idéntico a buildMediaMessage.
// v0.51.0.
func buildMediaMessageWithReply(ctx context.Context, client *whatsmeow.Client, kind, mimetype, filename, caption string, data []byte, quotedWAID, quotedSenderJID, quotedText string) (*waE2E.Message, error) {
	msg, err := buildMediaMessage(ctx, client, kind, mimetype, filename, caption, data)
	if err != nil {
		return nil, err
	}
	if quotedWAID == "" {
		return msg, nil
	}
	ci := &waE2E.ContextInfo{
		StanzaID: proto.String(quotedWAID),
		QuotedMessage: &waE2E.Message{
			Conversation: proto.String(quotedText),
		},
	}
	if quotedSenderJID != "" {
		ci.Participant = proto.String(quotedSenderJID)
	}
	// Cada tipo de media tiene su propio ContextInfo field.
	switch {
	case msg.ImageMessage != nil:
		msg.ImageMessage.ContextInfo = ci
	case msg.AudioMessage != nil:
		msg.AudioMessage.ContextInfo = ci
	case msg.VideoMessage != nil:
		msg.VideoMessage.ContextInfo = ci
	case msg.DocumentMessage != nil:
		msg.DocumentMessage.ContextInfo = ci
	}
	return msg, nil
}

// buildMediaMessage sube `data` al servidor de WhatsApp y construye el
// *waE2E.Message apropiado (Image/Audio/Video/Document) según `kind`.
func buildMediaMessage(ctx context.Context, client *whatsmeow.Client, kind, mimetype, filename, caption string, data []byte) (*waE2E.Message, error) {
	wmType, err := mediaTypeFor(kind)
	if err != nil {
		return nil, err
	}
	up, err := client.Upload(ctx, data, wmType)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	switch normalizeKind(kind) {
	case "image":
		return &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           proto.String(up.URL),
				DirectPath:    proto.String(up.DirectPath),
				MediaKey:      up.MediaKey,
				FileEncSHA256: up.FileEncSHA256,
				FileSHA256:    up.FileSHA256,
				FileLength:    proto.Uint64(up.FileLength),
				Mimetype:      proto.String(orDefault(mimetype, "image/jpeg")),
				Caption:       optionalString(caption),
			},
		}, nil
	case "audio":
		ptt := strings.Contains(mimetype, "opus") || strings.HasSuffix(strings.ToLower(filename), ".opus")
		return &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				URL:           proto.String(up.URL),
				DirectPath:    proto.String(up.DirectPath),
				MediaKey:      up.MediaKey,
				FileEncSHA256: up.FileEncSHA256,
				FileSHA256:    up.FileSHA256,
				FileLength:    proto.Uint64(up.FileLength),
				Mimetype:      proto.String(orDefault(mimetype, "audio/ogg; codecs=opus")),
				PTT:           proto.Bool(ptt),
			},
		}, nil
	case "video":
		return &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				URL:           proto.String(up.URL),
				DirectPath:    proto.String(up.DirectPath),
				MediaKey:      up.MediaKey,
				FileEncSHA256: up.FileEncSHA256,
				FileSHA256:    up.FileSHA256,
				FileLength:    proto.Uint64(up.FileLength),
				Mimetype:      proto.String(orDefault(mimetype, "video/mp4")),
				Caption:       optionalString(caption),
			},
		}, nil
	case "document":
		return &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           proto.String(up.URL),
				DirectPath:    proto.String(up.DirectPath),
				MediaKey:      up.MediaKey,
				FileEncSHA256: up.FileEncSHA256,
				FileSHA256:    up.FileSHA256,
				FileLength:    proto.Uint64(up.FileLength),
				Mimetype:      proto.String(orDefault(mimetype, "application/octet-stream")),
				FileName:      optionalString(filename),
				Caption:       optionalString(caption),
			},
		}, nil
	default:
		return nil, fmt.Errorf("media kind no soportado: %q", kind)
	}
}

// mediaTypeFor mapea nuestro "kind" string al MediaType de whatsmeow para Upload.
func mediaTypeFor(kind string) (whatsmeow.MediaType, error) {
	switch normalizeKind(kind) {
	case "image":
		return whatsmeow.MediaImage, nil
	case "audio":
		return whatsmeow.MediaAudio, nil
	case "video":
		return whatsmeow.MediaVideo, nil
	case "document":
		return whatsmeow.MediaDocument, nil
	}
	return "", fmt.Errorf("media kind desconocido: %q (image|audio|video|document)", kind)
}

// normalizeKind acepta sinónimos comunes (e.g. "file" → "document").
func normalizeKind(k string) string {
	switch strings.ToLower(k) {
	case "image", "img", "photo":
		return "image"
	case "audio", "voice":
		return "audio"
	case "video":
		return "video"
	case "document", "file", "doc":
		return "document"
	}
	return strings.ToLower(k)
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}
