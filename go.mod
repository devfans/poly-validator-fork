module poly-validator

go 1.15

require (
	github.com/beego/beego/v2 v2.0.1
	github.com/boltdb/bolt v1.3.1
	github.com/ethereum/go-ethereum v1.9.15
	github.com/urfave/cli/v2 v2.3.0
	poly-bridge v0.0.0-00010101000000-000000000000
)

replace poly-bridge => github.com/polynetwork/poly-bridge v0.0.0-20210713095810-ccab0e4a9a61
