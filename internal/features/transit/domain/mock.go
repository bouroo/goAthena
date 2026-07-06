package domain

//go:generate go run go.uber.org/mock/mockgen -destination=mock/transit_service_mock.go -package=domainmock . TransitService
//go:generate go run go.uber.org/mock/mockgen -destination=mock/transit_messenger_mock.go -package=domainmock . TransitMessenger
//go:generate go run go.uber.org/mock/mockgen -destination=mock/login_id_generator_mock.go -package=domainmock . LoginIDGenerator
//go:generate go run go.uber.org/mock/mockgen -destination=mock/transit_config_source_mock.go -package=domainmock . TransitConfigSource
