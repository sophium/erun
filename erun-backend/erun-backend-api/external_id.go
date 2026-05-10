package backendapi

import (
	"fmt"

	"github.com/google/uuid"
)

func NewExternalID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func ValidateExternalID(value string) error {
	id, err := uuid.Parse(value)
	if err != nil {
		return err
	}
	if id.Version() != 7 {
		return fmt.Errorf("external id must be uuidv7")
	}
	return nil
}
