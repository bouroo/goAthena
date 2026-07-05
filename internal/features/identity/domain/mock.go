package domain

//go:generate mockgen -destination=../repository/mock/account_repository_mock.go -package=mocks . AccountRepository
//go:generate mockgen -destination=../repository/mock/character_repository_mock.go -package=mocks . CharacterRepository
//go:generate mockgen -destination=../repository/mock/session_repository_mock.go -package=mocks . SessionRepository
//go:generate mockgen -destination=../repository/mock/warehouse_lock_mock.go -package=mocks . WarehouseLock
//go:generate mockgen -destination=mock/identity_service_mock.go -package=domainmock . IdentityService
