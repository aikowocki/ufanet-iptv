// Package admin реализует управление пользователями, каналами, пакетами и подписками.
package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/usecase"
)

// Ограничения на входные данные.
const (
	minLoginLen    = 3
	maxLoginLen    = 64
	minPasswordLen = 8
	maxPasswordLen = 128
	maxNameLen     = 100
	maxPathLen     = 255
	maxChannelNum  = 1_000_000
	maxPackageSize = 1000

	// DefaultListLimit и MaxListLimit задают размер страницы списка пользователей.
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Логин входит в Basic Auth, где двоеточие запрещено, поэтому набор символов ограничен.
var loginRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Repository — хранилище админских данных. Реализации обязаны возвращать
// domain.ErrNotFound для отсутствующей записи и domain.ErrConflict при нарушении уникальности.
type Repository interface {
	ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error)
	GetUser(ctx context.Context, id int64) (domain.User, error)
	CreateUser(ctx context.Context, u domain.User) (domain.User, error)
	UpdateUser(ctx context.Context, id int64, p UserPatch) (domain.User, error)

	ListChannels(ctx context.Context) ([]domain.Channel, error)
	CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error)
	UpdateChannel(ctx context.Context, id int64, p ChannelPatch) (domain.Channel, error)

	ListPackages(ctx context.Context) ([]domain.Package, error)
	GetPackage(ctx context.Context, id int64) (domain.Package, error)
	CreatePackage(ctx context.Context, p domain.Package) (domain.Package, error)
	UpdatePackage(ctx context.Context, id int64, p PackagePatch) (domain.Package, error)
	// SetPackageChannels заменяет состав пакета. Неизвестный канал даёт *domain.ReferenceError.
	SetPackageChannels(ctx context.Context, packageID int64, channelIDs []int64) error

	ListUserSubscriptions(ctx context.Context, userID int64) ([]domain.UserSubscription, error)
	CreateSubscription(ctx context.Context, s domain.UserSubscription) (domain.UserSubscription, error)
	UpdateSubscription(ctx context.Context, id int64, p SubscriptionPatch) (domain.UserSubscription, error)
}

// PasswordHasher считает хэш пароля. При перегрузке возвращает ошибку с domain.ErrBusy.
type PasswordHasher interface {
	Hash(ctx context.Context, password string) (string, error)
}

// UserPatch — частичное обновление пользователя на уровне хранилища. nil означает «не менять».
type UserPatch struct {
	IsActive     *bool
	IsAdmin      *bool
	PasswordHash *string
}

// ChannelPatch — частичное обновление канала.
type ChannelPatch struct {
	Number     *int
	Name       *string
	StreamPath *string
	IsActive   *bool
}

// PackagePatch — частичное обновление пакета.
type PackagePatch struct {
	Name     *string
	IsActive *bool
}

// SubscriptionPatch — частичное обновление подписки.
type SubscriptionPatch struct {
	IsActive  *bool
	ExpiresAt domain.OptionalTime
}

// UserInput — данные нового пользователя.
type UserInput struct {
	Login    string
	Password string
	IsAdmin  bool
	// IsActive по умолчанию true, если не задан.
	IsActive *bool
}

// UserUpdate — частичное обновление пользователя от клиента. Пароль передаётся открытым текстом.
type UserUpdate struct {
	IsActive *bool
	IsAdmin  *bool
	Password *string
}

// ChannelInput — данные нового канала.
type ChannelInput struct {
	Number     int
	Name       string
	StreamPath string
	IsActive   *bool
}

// PackageInput — данные нового пакета.
type PackageInput struct {
	Name     string
	IsActive *bool
}

// SubscriptionInput — данные новой подписки.
type SubscriptionInput struct {
	PackageID int64
	IsActive  *bool
	ExpiresAt domain.OptionalTime
}

// Service — админские сценарии.
type Service struct {
	repo   Repository
	tx     usecase.TxManager
	hasher PasswordHasher
}

// New создаёт Service.
func New(repo Repository, tx usecase.TxManager, hasher PasswordHasher) *Service {
	return &Service{repo: repo, tx: tx, hasher: hasher}
}

func invalid(format string, args ...any) error {
	return &domain.ValidationError{Message: fmt.Sprintf(format, args...)}
}

// ListUsers возвращает страницу пользователей по возрастанию id.
func (s *Service) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)
	if offset < 0 {
		return nil, invalid("offset must not be negative")
	}
	list, err := s.repo.ListUsers(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return list, nil
}

// GetUser возвращает пользователя по id.
func (s *Service) GetUser(ctx context.Context, id int64) (domain.User, error) {
	u, err := s.repo.GetUser(ctx, id)
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// CreateUser создаёт пользователя. Занятый логин даёт domain.ErrConflict.
func (s *Service) CreateUser(ctx context.Context, in UserInput) (domain.User, error) {
	if err := validateLogin(in.Login); err != nil {
		return domain.User{}, err
	}
	if err := validatePassword(in.Password); err != nil {
		return domain.User{}, err
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	u, err := s.repo.CreateUser(ctx, domain.User{
		Login: in.Login, PasswordHash: hash, IsAdmin: in.IsAdmin, IsActive: boolOr(in.IsActive, true),
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// UpdateUser меняет флаги и пароль пользователя. actor — администратор, выполняющий запрос:
// он не может выключить или разжаловать самого себя, иначе сервис останется без управления.
func (s *Service) UpdateUser(ctx context.Context, actor domain.User, id int64, in UserUpdate) (domain.User, error) {
	if in.IsActive == nil && in.IsAdmin == nil && in.Password == nil {
		return domain.User{}, invalid("no fields to update")
	}
	if actor.ID == id && ((in.IsActive != nil && !*in.IsActive) || (in.IsAdmin != nil && !*in.IsAdmin)) {
		return domain.User{}, domain.ErrSelfLockout
	}
	patch := UserPatch{IsActive: in.IsActive, IsAdmin: in.IsAdmin}
	if in.Password != nil {
		if err := validatePassword(*in.Password); err != nil {
			return domain.User{}, err
		}
		// Хэш считается до транзакции: argon2id тяжёлый и не должен держать соединение с БД.
		hash, err := s.hasher.Hash(ctx, *in.Password)
		if err != nil {
			return domain.User{}, fmt.Errorf("hash password: %w", err)
		}
		patch.PasswordHash = &hash
	}
	u, err := s.repo.UpdateUser(ctx, id, patch)
	if err != nil {
		return domain.User{}, fmt.Errorf("update user: %w", err)
	}
	return u, nil
}

// ListChannels возвращает все каналы, включая выключенные.
func (s *Service) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	list, err := s.repo.ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return list, nil
}

// CreateChannel создаёт канал. Занятый номер даёт domain.ErrConflict.
func (s *Service) CreateChannel(ctx context.Context, in ChannelInput) (domain.Channel, error) {
	if err := validateNumber(in.Number); err != nil {
		return domain.Channel{}, err
	}
	if err := validateName("name", in.Name); err != nil {
		return domain.Channel{}, err
	}
	if err := validateStreamPath(in.StreamPath); err != nil {
		return domain.Channel{}, err
	}
	c, err := s.repo.CreateChannel(ctx, domain.Channel{
		Number: in.Number, Name: in.Name, StreamPath: in.StreamPath, IsActive: boolOr(in.IsActive, true),
	})
	if err != nil {
		return domain.Channel{}, fmt.Errorf("create channel: %w", err)
	}
	return c, nil
}

// UpdateChannel меняет заданные поля канала.
func (s *Service) UpdateChannel(ctx context.Context, id int64, p ChannelPatch) (domain.Channel, error) {
	if p.Number == nil && p.Name == nil && p.StreamPath == nil && p.IsActive == nil {
		return domain.Channel{}, invalid("no fields to update")
	}
	if p.Number != nil {
		if err := validateNumber(*p.Number); err != nil {
			return domain.Channel{}, err
		}
	}
	if p.Name != nil {
		if err := validateName("name", *p.Name); err != nil {
			return domain.Channel{}, err
		}
	}
	if p.StreamPath != nil {
		if err := validateStreamPath(*p.StreamPath); err != nil {
			return domain.Channel{}, err
		}
	}
	c, err := s.repo.UpdateChannel(ctx, id, p)
	if err != nil {
		return domain.Channel{}, fmt.Errorf("update channel: %w", err)
	}
	return c, nil
}

// ListPackages возвращает все пакеты с составом.
func (s *Service) ListPackages(ctx context.Context) ([]domain.Package, error) {
	list, err := s.repo.ListPackages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	return list, nil
}

// CreatePackage создаёт пустой пакет. Занятое имя даёт domain.ErrConflict.
func (s *Service) CreatePackage(ctx context.Context, in PackageInput) (domain.Package, error) {
	if err := validateName("name", in.Name); err != nil {
		return domain.Package{}, err
	}
	p, err := s.repo.CreatePackage(ctx, domain.Package{Name: in.Name, IsActive: boolOr(in.IsActive, true)})
	if err != nil {
		return domain.Package{}, fmt.Errorf("create package: %w", err)
	}
	return p, nil
}

// UpdatePackage меняет имя или флаг пакета.
func (s *Service) UpdatePackage(ctx context.Context, id int64, p PackagePatch) (domain.Package, error) {
	if p.Name == nil && p.IsActive == nil {
		return domain.Package{}, invalid("no fields to update")
	}
	if p.Name != nil {
		if err := validateName("name", *p.Name); err != nil {
			return domain.Package{}, err
		}
	}
	out, err := s.repo.UpdatePackage(ctx, id, p)
	if err != nil {
		return domain.Package{}, fmt.Errorf("update package: %w", err)
	}
	return out, nil
}

// SetPackageChannels заменяет состав пакета и возвращает обновлённый пакет.
// Проверка существования, замена и чтение идут в одной транзакции, поэтому
// клиент никогда не увидит наполовину изменённый состав.
func (s *Service) SetPackageChannels(ctx context.Context, packageID int64, channelIDs []int64) (domain.Package, error) {
	if len(channelIDs) > maxPackageSize {
		return domain.Package{}, invalid("a package holds at most %d channels", maxPackageSize)
	}
	ids := slices.Clone(channelIDs)
	for _, id := range ids {
		if id <= 0 {
			return domain.Package{}, invalid("channel ids must be positive")
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)

	var out domain.Package
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetPackage(ctx, packageID); err != nil {
			return err
		}
		if err := s.repo.SetPackageChannels(ctx, packageID, ids); err != nil {
			return err
		}
		p, err := s.repo.GetPackage(ctx, packageID)
		out = p
		return err
	})
	if err != nil {
		return domain.Package{}, fmt.Errorf("set package channels: %w", err)
	}
	return out, nil
}

// ListUserSubscriptions возвращает подписки пользователя. Несуществующий пользователь даёт domain.ErrNotFound.
func (s *Service) ListUserSubscriptions(ctx context.Context, userID int64) ([]domain.UserSubscription, error) {
	var out []domain.UserSubscription
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetUser(ctx, userID); err != nil {
			return err
		}
		list, err := s.repo.ListUserSubscriptions(ctx, userID)
		out = list
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list user subscriptions: %w", err)
	}
	return out, nil
}

// CreateSubscription выдаёт пользователю пакет. Повторная выдача того же пакета
// даёт domain.ErrConflict: срок и флаг меняются через UpdateSubscription.
func (s *Service) CreateSubscription(ctx context.Context, userID int64, in SubscriptionInput) (domain.UserSubscription, error) {
	if in.PackageID <= 0 {
		return domain.UserSubscription{}, invalid("package_id is required")
	}
	var out domain.UserSubscription
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetUser(ctx, userID); err != nil {
			return err
		}
		if _, err := s.repo.GetPackage(ctx, in.PackageID); err != nil {
			if isNotFound(err) {
				return &domain.ReferenceError{Message: fmt.Sprintf("package %d does not exist", in.PackageID)}
			}
			return err
		}
		sub, err := s.repo.CreateSubscription(ctx, domain.UserSubscription{
			UserID: userID, PackageID: in.PackageID, IsActive: boolOr(in.IsActive, true), ExpiresAt: in.ExpiresAt.Value,
		})
		out = sub
		return err
	})
	if err != nil {
		return domain.UserSubscription{}, fmt.Errorf("create subscription: %w", err)
	}
	return out, nil
}

// UpdateSubscription меняет флаг и срок подписки.
func (s *Service) UpdateSubscription(ctx context.Context, id int64, p SubscriptionPatch) (domain.UserSubscription, error) {
	if p.IsActive == nil && !p.ExpiresAt.Set {
		return domain.UserSubscription{}, invalid("no fields to update")
	}
	sub, err := s.repo.UpdateSubscription(ctx, id, p)
	if err != nil {
		return domain.UserSubscription{}, fmt.Errorf("update subscription: %w", err)
	}
	return sub, nil
}

func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func validateLogin(login string) error {
	if n := len(login); n < minLoginLen || n > maxLoginLen {
		return invalid("login must be %d-%d characters", minLoginLen, maxLoginLen)
	}
	if !loginRe.MatchString(login) {
		return invalid("login may contain only latin letters, digits, '.', '_' and '-'")
	}
	return nil
}

func validatePassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < minPasswordLen || len(pw) > maxPasswordLen {
		return invalid("password must be %d-%d characters", minPasswordLen, maxPasswordLen)
	}
	return nil
}

func validateNumber(n int) error {
	if n <= 0 || n > maxChannelNum {
		return invalid("number must be between 1 and %d", maxChannelNum)
	}
	return nil
}

func validateName(field, name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > maxNameLen {
		return invalid("%s must be 1-%d characters", field, maxNameLen)
	}
	if !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return invalid("%s contains invalid characters", field)
	}
	return nil
}

// validateStreamPath принимает только относительный путь: он дописывается к STREAM_BASE_URL,
// поэтому схема, query, fragment и выход через «..» недопустимы.
func validateStreamPath(p string) error {
	if p == "" || len(p) > maxPathLen {
		return invalid("stream_path must be 1-%d characters", maxPathLen)
	}
	if strings.ContainsAny(p, "?#\\ ") || strings.Contains(p, "://") || strings.HasPrefix(p, "//") ||
		strings.ContainsFunc(p, unicode.IsControl) {
		return invalid("stream_path must be a plain relative path")
	}
	if slices.Contains(strings.Split(p, "/"), "..") {
		return invalid("stream_path must not contain '..'")
	}
	return nil
}
