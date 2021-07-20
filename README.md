# poly-validator
Poly cross chain transaction validator

### Get Started

Poly validator is used to scan the target chain cross chain transactions to validate with merkle proofs in poly chain.

#### Compile
```
go build -tags mainnet(or testnet) .
```

#### Commmand line

```
NAME:
   Poly Validator - Poly cross chain transaction validator

USAGE:
   main [global options] command [command options] [arguments...]

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --config value  configuration file (default: "config.json")
   --chain value   chain to monitor, default: all cchains specified in config file (default: 0)
   --help, -h      show help (default: false)
```
