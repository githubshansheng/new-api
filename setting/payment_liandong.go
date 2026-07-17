package setting

const (
	DefaultLiandongJUUID                     = ""
	DefaultLiandongPollIntervalSeconds       = 30
	MinLiandongPollIntervalSeconds           = 1
	MaxLiandongPollIntervalSeconds           = 3600
	DefaultLiandongClientPollIntervalSeconds = 5
	MinLiandongClientPollIntervalSeconds     = 1
	MaxLiandongClientPollIntervalSeconds     = 60
	DefaultLiandongReconcileBatchSize        = 50
	MinLiandongReconcileBatchSize            = 1
	MaxLiandongReconcileBatchSize            = 500
	DefaultLiandongPaymentTimeoutMinutes     = 30
	MinLiandongPaymentTimeoutMinutes         = 1
	MaxLiandongPaymentTimeoutMinutes         = 1440

	LiandongAuthModeManualToken = "manual_token"
	LiandongAuthModeCredentials = "credentials"
)

type LiandongPaymentSettings struct {
	Enabled                   bool
	CreateEnabled             bool
	ReconcileEnabled          bool
	FulfillEnabled            bool
	IframeEnabled             bool
	PollIntervalSeconds       int
	ClientPollIntervalSeconds int
	ReconcileBatchSize        int
	PaymentTimeoutMinutes     int
	JUUID                     string
	AuthMode                  string
	Username                  string
	Password                  string
	MerchantToken             string
}
