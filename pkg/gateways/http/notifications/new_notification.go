package notifications

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/stone-co/webhook-consumer/pkg/domain"
	"github.com/stone-co/webhook-consumer/pkg/gateways/http/responses"
	"gopkg.in/square/go-jose.v2"
)

const (
	EventIDHeader   = "X-Stone-Webhook-Event-Id"
	EventTypeHeader = "X-Stone-Webhook-Event-Type"
)

type NotificationRequest struct {
	EncryptedBody string `json:"encrypted_body" validate:"required"`
}

func (h Handler) New(w http.ResponseWriter, r *http.Request) {
	// Decode request body.
	var encryptedBody NotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&encryptedBody); err != nil {
		h.log.WithError(err).Error("body is empty or has no valid fields")
		_ = responses.SendError(w, "body is empty or has no valid fields", http.StatusBadRequest)
		return
	}

	// Validate request body.
	if err := h.Validate(encryptedBody); err != nil {
		h.log.WithError(err).Error("invalid request body")
		_ = responses.SendError(w, err.Error(), http.StatusBadRequest)
		return
	}

	encryptedPayload, err := h.verify(encryptedBody.EncryptedBody)
	if err != nil {
		h.log.WithError(err).Error("invalid signature")
		_ = responses.SendError(w, err.Error(), http.StatusForbidden)
		return
	}

	payload, err := h.decode(encryptedPayload)
	if err != nil {
		h.log.WithError(err).Error("invalid payload")
		_ = responses.SendError(w, err.Error(), http.StatusForbidden)
		return
	}

	input := domain.NotificationInput{
		Header: domain.HeaderNotification{
			EventID:   r.Header.Get(EventIDHeader),
			EventType: r.Header.Get(EventTypeHeader),
		},
		Body: payload,
	}

	// Call the usecase.
	err = h.usecase.SendNotification(r.Context(), input)
	if err != nil {
		h.log.WithError(err).Error("failed to send notification")
		_ = responses.SendError(w, "failed to send notification", http.StatusInternalServerError)
		return
	}

	_ = responses.Send(w, nil, http.StatusNoContent)
}

func (h Handler) verify(signedBody string) (string, error) {
	obj, err := jose.ParseSigned(signedBody)
	if err != nil {
		return "", fmt.Errorf("unable to parse message: %v", err)
	}

	if len(obj.Signatures) != 1 {
		return "", fmt.Errorf("multi signature not supported")
	}

	// signature := obj.Signatures[0]

	plaintext, err := obj.Verify(h.verificationKeyList[0]) // TODO: ?
	if err != nil {
		return "", fmt.Errorf("invalid signature: %v", err)
	}

	return string(plaintext), nil
}

func (h Handler) decode(encryptedBody string) (string, error) {
	// Parse the serialized, encrypted JWE object. An error would indicate that
	// the given input did not represent a valid message.
	object, err := jose.ParseEncrypted(encryptedBody)
	if err != nil {
		return "", fmt.Errorf("parsing encrypted: %v", err)
	}

	// Now we can decrypt and get back our original plaintext. An error here
	// would indicate the the message failed to decrypt, e.g. because the auth
	// tag was broken or the message was tampered with.
	decrypted, err := object.Decrypt(h.privateKey)
	if err != nil {
		return "", fmt.Errorf("decrypting: %v", err)
	}

	return string(decrypted), nil
}