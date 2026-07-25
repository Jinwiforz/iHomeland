package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
)

// maximumCertificateBytes 将公开证书链限制为1 MiB，避免启动期读取异常文件造成无界内存占用。
const maximumCertificateBytes = 1024 * 1024

// preparedPublicAPI 保存产生网络副作用前已经验证的 TLS identity 与短生命周期 derivation key。
type preparedPublicAPI struct {
	// tlsConfig 只保存解析后的 certificate/private-key 对象，不保留 PEM bytes。
	tlsConfig *tls.Config
	// derivationKey 必须在public component Start返回前清零，成功时service已复制所需材料。
	derivationKey []byte
	// battleDerivationKey 必须在 BattleTicket issuer 复制后立即清零。
	battleDerivationKey []byte
}

// Destroy 清零尚未转移给 WorldAdmission service 的 derivation key 副本。
func (prepared *preparedPublicAPI) Destroy() {
	if prepared == nil {
		return
	}
	clear(prepared.derivationKey)
	prepared.derivationKey = nil
	clear(prepared.battleDerivationKey)
	prepared.battleDerivationKey = nil
}

// preparePublicAPI 在 logger、listener 与 storage client 创建前解析全部公开 secret material。
func preparePublicAPI(ctx context.Context, settings config.PublicAPI, provider secret.Provider) (preparedPublicAPI, error) {
	derivationValue, err := resolveSecret(ctx, provider, settings.WorldAdmission.DerivationKeySecret, "publicApi.worldAdmission.derivationKeySecret")
	if err != nil {
		return preparedPublicAPI{}, err
	}
	defer derivationValue.Destroy()
	prepared := preparedPublicAPI{}
	if err := derivationValue.Expose(func(content []byte) error {
		if len(content) < 32 {
			return errors.New("world admission derivation key must contain at least 32 bytes")
		}
		prepared.derivationKey = append([]byte(nil), content...)
		return nil
	}); err != nil {
		return preparedPublicAPI{}, fmt.Errorf("prepare publicApi.worldAdmission.derivationKeySecret: %w", err)
	}
	battleValue, err := resolveSecret(ctx, provider, settings.BattleUDP.DerivationKeySecret, "publicApi.battleUdp.derivationKeySecret")
	if err != nil {
		return preparedPublicAPI{}, err
	}
	defer battleValue.Destroy()
	if err := battleValue.Expose(func(content []byte) error {
		if len(content) != 32 {
			return errors.New("battle derivation key must contain exactly 32 bytes")
		}
		prepared.battleDerivationKey = append([]byte(nil), content...)
		return nil
	}); err != nil {
		return preparedPublicAPI{}, fmt.Errorf("prepare publicApi.battleUdp.derivationKeySecret: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			prepared.Destroy()
		}
	}()
	if settings.TLS.Enabled {
		certificatePEM, err := readBoundedCertificate(settings.TLS.CertificateFile)
		if err != nil {
			return preparedPublicAPI{}, fmt.Errorf("prepare publicApi.tls.certificateFile: read failed")
		}
		defer clear(certificatePEM)
		privateKey, err := resolveSecret(ctx, provider, settings.TLS.PrivateKeySecret, "publicApi.tls.privateKeySecret")
		if err != nil {
			return preparedPublicAPI{}, err
		}
		defer privateKey.Destroy()
		var certificate tls.Certificate
		if err := privateKey.Expose(func(keyPEM []byte) error {
			parsed, parseErr := tls.X509KeyPair(certificatePEM, keyPEM)
			if parseErr != nil {
				return errors.New("certificate and private key do not form a valid pair")
			}
			certificate = parsed
			return nil
		}); err != nil {
			return preparedPublicAPI{}, fmt.Errorf("prepare publicApi.tls identity: %w", err)
		}
		prepared.tlsConfig = &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{certificate},
		}
	}
	succeeded = true
	return prepared, nil
}

// readBoundedCertificate 读取公开证书链并限制配置文件内存占用。
func readBoundedCertificate(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumCertificateBytes+1))
	if err != nil {
		clear(contents)
		return nil, err
	}
	if len(contents) == 0 || len(contents) > maximumCertificateBytes {
		clear(contents)
		return nil, errors.New("certificate file has invalid size")
	}
	return contents, nil
}
