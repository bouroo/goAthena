package domain

//go:generate go run go.uber.org/mock/mockgen -destination=../repository/mock/account_repository_mock.go -package=mocks . AccountRepository
//go:generate go run go.uber.org/mock/mockgen -destination=../repository/mock/character_repository_mock.go -package=mocks . CharacterRepository
//go:generate go run go.uber.org/mock/mockgen -destination=../repository/mock/session_repository_mock.go -package=mocks . SessionRepository
//go:generate go run go.uber.org/mock/mockgen -destination=../repository/mock/warehouse_lock_mock.go -package=mocks . WarehouseLock
//go:generate go run go.uber.org/mock/mockgen -destination=mock/identity_service_mock.go -package=domainmock . IdentityService
