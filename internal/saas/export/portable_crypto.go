package export

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"golang.org/x/crypto/argon2"
	"io"
)

var portableMagic = []byte("AMPB2")

func EncryptPortable(passphrase string, plain []byte) ([]byte, error) {
	if len(passphrase) < 12 {
		return nil, errors.New("portable bundle passphrase must contain at least 12 characters")
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	header := append(append([]byte{}, portableMagic...), salt...)
	header = append(header, byte(len(nonce)))
	header = append(header, nonce...)
	sealed := gcm.Seal(nil, nonce, plain, header)
	result := make([]byte, 4+len(header)+len(sealed))
	binary.BigEndian.PutUint32(result[:4], uint32(len(header)))
	copy(result[4:], header)
	copy(result[4+len(header):], sealed)
	return result, nil
}
func DecryptPortable(passphrase string, value []byte) ([]byte, error) {
	if len(value) < 4 {
		return nil, errors.New("invalid portable bundle")
	}
	headerSize := int(binary.BigEndian.Uint32(value[:4]))
	if headerSize < 22 || 4+headerSize >= len(value) {
		return nil, errors.New("invalid portable bundle")
	}
	header := value[4 : 4+headerSize]
	if string(header[:5]) != string(portableMagic) {
		return nil, errors.New("unsupported portable bundle")
	}
	salt := header[5:21]
	nonceSize := int(header[21])
	if nonceSize <= 0 || 22+nonceSize != len(header) {
		return nil, errors.New("invalid portable bundle")
	}
	nonce := header[22:]
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, value[4+headerSize:], header)
	if err != nil {
		return nil, errors.New("portable bundle authentication failed")
	}
	return plain, nil
}
