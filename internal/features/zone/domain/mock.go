package domain

//go:generate go run go.uber.org/mock/mockgen -destination=mock/zone_service_mock.go -package=domainmock . ZoneService
//go:generate go run go.uber.org/mock/mockgen -destination=mock/map_repository_mock.go -package=domainmock . MapRepository
