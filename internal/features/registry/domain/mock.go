package domain

//go:generate go run go.uber.org/mock/mockgen -destination=mock/registry_mock.go -package=domainmock . Registry
//go:generate go run go.uber.org/mock/mockgen -destination=mock/store_mock.go -package=domainmock . Store
