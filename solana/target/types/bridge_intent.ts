/**
 * Program IDL in camelCase format in order to be used in JS/TS.
 *
 * Note that this is only a type helper and is not the actual IDL. The original
 * IDL can be found at `target/idl/bridge_intent.json`.
 */
export type BridgeIntent = {
  "address": "64SZByfhWxvxu7haf153RHBvReNUFqz154RNagBbeYwk",
  "metadata": {
    "name": "bridgeIntent",
    "version": "0.1.0",
    "spec": "0.1.0",
    "description": "Cross-chain bridging intent program — Solana side of the Tachyon BridgeIntent protocol"
  },
  "instructions": [
    {
      "name": "addRelayer",
      "discriminator": [
        184,
        240,
        94,
        199,
        19,
        71,
        21,
        192
      ],
      "accounts": [
        {
          "name": "owner",
          "writable": true,
          "signer": true,
          "relations": [
            "config"
          ]
        },
        {
          "name": "config",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "relayer"
        },
        {
          "name": "relayerAuth",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  114,
                  101,
                  108,
                  97,
                  121,
                  101,
                  114
                ]
              },
              {
                "kind": "account",
                "path": "relayer"
              }
            ]
          }
        },
        {
          "name": "systemProgram",
          "address": "11111111111111111111111111111111"
        }
      ],
      "args": []
    },
    {
      "name": "cancelIntent",
      "discriminator": [
        67,
        73,
        238,
        244,
        208,
        89,
        225,
        59
      ],
      "accounts": [
        {
          "name": "caller",
          "writable": true,
          "signer": true
        },
        {
          "name": "config",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "intentRecord",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  105,
                  110,
                  116,
                  101,
                  110,
                  116
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        },
        {
          "name": "tokenAMint"
        },
        {
          "name": "escrowAuthority",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  101,
                  115,
                  99,
                  114,
                  111,
                  119
                ]
              }
            ]
          }
        },
        {
          "name": "escrowTokenAAta",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "account",
                "path": "escrowAuthority"
              },
              {
                "kind": "const",
                "value": [
                  6,
                  221,
                  246,
                  225,
                  215,
                  101,
                  161,
                  147,
                  217,
                  203,
                  225,
                  70,
                  206,
                  235,
                  121,
                  172,
                  28,
                  180,
                  133,
                  237,
                  95,
                  91,
                  55,
                  145,
                  58,
                  140,
                  245,
                  133,
                  126,
                  255,
                  0,
                  169
                ]
              },
              {
                "kind": "account",
                "path": "tokenAMint"
              }
            ],
            "program": {
              "kind": "const",
              "value": [
                140,
                151,
                37,
                143,
                78,
                36,
                137,
                241,
                187,
                61,
                16,
                41,
                20,
                142,
                13,
                131,
                11,
                90,
                19,
                153,
                218,
                255,
                16,
                132,
                4,
                142,
                123,
                216,
                219,
                233,
                248,
                89
              ]
            }
          }
        },
        {
          "name": "user"
        },
        {
          "name": "userTokenAAta",
          "writable": true
        },
        {
          "name": "winningSolver",
          "docs": [
            "account here if no solver was selected (passes the constraint as a no-op",
            "because winning_bid = 0 will skip the refund)."
          ]
        },
        {
          "name": "winningSolverAta",
          "writable": true
        },
        {
          "name": "tokenProgram",
          "address": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
        },
        {
          "name": "associatedTokenProgram",
          "address": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
        }
      ],
      "args": [
        {
          "name": "intentId",
          "type": "u64"
        }
      ]
    },
    {
      "name": "createIntent",
      "discriminator": [
        216,
        214,
        79,
        121,
        23,
        194,
        96,
        104
      ],
      "accounts": [
        {
          "name": "user",
          "writable": true,
          "signer": true
        },
        {
          "name": "config",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "tokenAMint"
        },
        {
          "name": "tokenBMint",
          "docs": [
            "Token-B mint is recorded so the eventual destination delivery can be",
            "verified. For non-Solana destinations, callers SHOULD pass the",
            "`token_a_mint` here as a placeholder (Solana stores 32-byte pubkeys",
            "natively; the canonical destination token address travels in the",
            "off-chain message)."
          ]
        },
        {
          "name": "userTokenAAta",
          "writable": true
        },
        {
          "name": "escrowAuthority",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  101,
                  115,
                  99,
                  114,
                  111,
                  119
                ]
              }
            ]
          }
        },
        {
          "name": "escrowTokenAAta",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "account",
                "path": "escrowAuthority"
              },
              {
                "kind": "const",
                "value": [
                  6,
                  221,
                  246,
                  225,
                  215,
                  101,
                  161,
                  147,
                  217,
                  203,
                  225,
                  70,
                  206,
                  235,
                  121,
                  172,
                  28,
                  180,
                  133,
                  237,
                  95,
                  91,
                  55,
                  145,
                  58,
                  140,
                  245,
                  133,
                  126,
                  255,
                  0,
                  169
                ]
              },
              {
                "kind": "account",
                "path": "tokenAMint"
              }
            ],
            "program": {
              "kind": "const",
              "value": [
                140,
                151,
                37,
                143,
                78,
                36,
                137,
                241,
                187,
                61,
                16,
                41,
                20,
                142,
                13,
                131,
                11,
                90,
                19,
                153,
                218,
                255,
                16,
                132,
                4,
                142,
                123,
                216,
                219,
                233,
                248,
                89
              ]
            }
          }
        },
        {
          "name": "intentRecord",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  105,
                  110,
                  116,
                  101,
                  110,
                  116
                ]
              },
              {
                "kind": "account",
                "path": "config.next_intent_id",
                "account": "config"
              }
            ]
          }
        },
        {
          "name": "intentBids",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  98,
                  105,
                  100,
                  115
                ]
              },
              {
                "kind": "account",
                "path": "config.next_intent_id",
                "account": "config"
              }
            ]
          }
        },
        {
          "name": "tokenProgram",
          "address": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
        },
        {
          "name": "associatedTokenProgram",
          "address": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
        },
        {
          "name": "systemProgram",
          "address": "11111111111111111111111111111111"
        },
        {
          "name": "rent",
          "address": "SysvarRent111111111111111111111111111111111"
        }
      ],
      "args": [
        {
          "name": "amountA",
          "type": "u64"
        },
        {
          "name": "expectedAmountB",
          "type": "u64"
        },
        {
          "name": "reward",
          "type": "u64"
        },
        {
          "name": "destChainId",
          "type": "u64"
        },
        {
          "name": "recipient",
          "type": {
            "array": [
              "u8",
              32
            ]
          }
        },
        {
          "name": "auctionDuration",
          "type": "u64"
        }
      ]
    },
    {
      "name": "depositAndPickup",
      "discriminator": [
        49,
        115,
        70,
        75,
        171,
        86,
        224,
        249
      ],
      "accounts": [
        {
          "name": "solver",
          "writable": true,
          "signer": true
        },
        {
          "name": "config",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "intentRecord",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  105,
                  110,
                  116,
                  101,
                  110,
                  116
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        },
        {
          "name": "tokenAMint"
        },
        {
          "name": "solverTokenAAta",
          "writable": true
        },
        {
          "name": "escrowAuthority",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  101,
                  115,
                  99,
                  114,
                  111,
                  119
                ]
              }
            ]
          }
        },
        {
          "name": "escrowTokenAAta",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "account",
                "path": "escrowAuthority"
              },
              {
                "kind": "const",
                "value": [
                  6,
                  221,
                  246,
                  225,
                  215,
                  101,
                  161,
                  147,
                  217,
                  203,
                  225,
                  70,
                  206,
                  235,
                  121,
                  172,
                  28,
                  180,
                  133,
                  237,
                  95,
                  91,
                  55,
                  145,
                  58,
                  140,
                  245,
                  133,
                  126,
                  255,
                  0,
                  169
                ]
              },
              {
                "kind": "account",
                "path": "tokenAMint"
              }
            ],
            "program": {
              "kind": "const",
              "value": [
                140,
                151,
                37,
                143,
                78,
                36,
                137,
                241,
                187,
                61,
                16,
                41,
                20,
                142,
                13,
                131,
                11,
                90,
                19,
                153,
                218,
                255,
                16,
                132,
                4,
                142,
                123,
                216,
                219,
                233,
                248,
                89
              ]
            }
          }
        },
        {
          "name": "tokenProgram",
          "address": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
        },
        {
          "name": "associatedTokenProgram",
          "address": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
        }
      ],
      "args": [
        {
          "name": "intentId",
          "type": "u64"
        }
      ]
    },
    {
      "name": "finalizeAuction",
      "discriminator": [
        220,
        209,
        175,
        193,
        57,
        132,
        241,
        168
      ],
      "accounts": [
        {
          "name": "caller",
          "signer": true
        },
        {
          "name": "intentRecord",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  105,
                  110,
                  116,
                  101,
                  110,
                  116
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        },
        {
          "name": "intentBids",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  98,
                  105,
                  100,
                  115
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        }
      ],
      "args": [
        {
          "name": "intentId",
          "type": "u64"
        }
      ]
    },
    {
      "name": "initialize",
      "discriminator": [
        175,
        175,
        109,
        31,
        13,
        152,
        155,
        237
      ],
      "accounts": [
        {
          "name": "payer",
          "writable": true,
          "signer": true
        },
        {
          "name": "config",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "escrowAuthority",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  101,
                  115,
                  99,
                  114,
                  111,
                  119
                ]
              }
            ]
          }
        },
        {
          "name": "systemProgram",
          "address": "11111111111111111111111111111111"
        }
      ],
      "args": [
        {
          "name": "chainId",
          "type": "u64"
        },
        {
          "name": "allowedTokenBMints",
          "type": {
            "array": [
              "pubkey",
              2
            ]
          }
        }
      ]
    },
    {
      "name": "placeBid",
      "discriminator": [
        238,
        77,
        148,
        91,
        200,
        151,
        92,
        146
      ],
      "accounts": [
        {
          "name": "solver",
          "signer": true
        },
        {
          "name": "intentRecord",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  105,
                  110,
                  116,
                  101,
                  110,
                  116
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        },
        {
          "name": "intentBids",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  98,
                  105,
                  100,
                  115
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        }
      ],
      "args": [
        {
          "name": "intentId",
          "type": "u64"
        },
        {
          "name": "bidAmount",
          "type": "u64"
        }
      ]
    },
    {
      "name": "registerStealth",
      "discriminator": [
        168,
        238,
        218,
        4,
        158,
        79,
        4,
        63
      ],
      "accounts": [
        {
          "name": "user",
          "writable": true,
          "signer": true
        },
        {
          "name": "stealthRegistration",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  115,
                  116,
                  101,
                  97,
                  108,
                  116,
                  104
                ]
              },
              {
                "kind": "account",
                "path": "user"
              },
              {
                "kind": "arg",
                "path": "ephemeralSalt"
              }
            ]
          }
        },
        {
          "name": "systemProgram",
          "address": "11111111111111111111111111111111"
        }
      ],
      "args": [
        {
          "name": "ephemeralSalt",
          "type": {
            "array": [
              "u8",
              32
            ]
          }
        }
      ]
    },
    {
      "name": "removeRelayer",
      "discriminator": [
        154,
        149,
        161,
        231,
        69,
        74,
        136,
        237
      ],
      "accounts": [
        {
          "name": "owner",
          "writable": true,
          "signer": true,
          "relations": [
            "config"
          ]
        },
        {
          "name": "config",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "relayer",
          "relations": [
            "relayerAuth"
          ]
        },
        {
          "name": "relayerAuth",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  114,
                  101,
                  108,
                  97,
                  121,
                  101,
                  114
                ]
              },
              {
                "kind": "account",
                "path": "relayer"
              }
            ]
          }
        }
      ],
      "args": []
    },
    {
      "name": "setAllowedTokenBMint",
      "discriminator": [
        254,
        58,
        230,
        66,
        57,
        70,
        227,
        98
      ],
      "accounts": [
        {
          "name": "owner",
          "signer": true,
          "relations": [
            "config"
          ]
        },
        {
          "name": "config",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        }
      ],
      "args": [
        {
          "name": "index",
          "type": "u8"
        },
        {
          "name": "mint",
          "type": "pubkey"
        }
      ]
    },
    {
      "name": "settleIntentWithChain2Verification",
      "discriminator": [
        7,
        42,
        140,
        48,
        21,
        30,
        233,
        168
      ],
      "accounts": [
        {
          "name": "relayer",
          "writable": true,
          "signer": true
        },
        {
          "name": "config",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "relayerAuth",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  114,
                  101,
                  108,
                  97,
                  121,
                  101,
                  114
                ]
              },
              {
                "kind": "account",
                "path": "relayer"
              }
            ]
          }
        },
        {
          "name": "intentRecord",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  105,
                  110,
                  116,
                  101,
                  110,
                  116
                ]
              },
              {
                "kind": "arg",
                "path": "intentId"
              }
            ]
          }
        },
        {
          "name": "tokenAMint"
        },
        {
          "name": "escrowAuthority",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  101,
                  115,
                  99,
                  114,
                  111,
                  119
                ]
              }
            ]
          }
        },
        {
          "name": "escrowTokenAAta",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "account",
                "path": "escrowAuthority"
              },
              {
                "kind": "const",
                "value": [
                  6,
                  221,
                  246,
                  225,
                  215,
                  101,
                  161,
                  147,
                  217,
                  203,
                  225,
                  70,
                  206,
                  235,
                  121,
                  172,
                  28,
                  180,
                  133,
                  237,
                  95,
                  91,
                  55,
                  145,
                  58,
                  140,
                  245,
                  133,
                  126,
                  255,
                  0,
                  169
                ]
              },
              {
                "kind": "account",
                "path": "tokenAMint"
              }
            ],
            "program": {
              "kind": "const",
              "value": [
                140,
                151,
                37,
                143,
                78,
                36,
                137,
                241,
                187,
                61,
                16,
                41,
                20,
                142,
                13,
                131,
                11,
                90,
                19,
                153,
                218,
                255,
                16,
                132,
                4,
                142,
                123,
                216,
                219,
                233,
                248,
                89
              ]
            }
          }
        },
        {
          "name": "winningSolver",
          "docs": [
            "Solver receiving the released funds. Must match `intent_record.winning_solver`."
          ]
        },
        {
          "name": "winningSolverAta",
          "writable": true
        },
        {
          "name": "tokenProgram",
          "address": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
        },
        {
          "name": "associatedTokenProgram",
          "address": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
        }
      ],
      "args": [
        {
          "name": "intentId",
          "type": "u64"
        },
        {
          "name": "chain2IntentId",
          "type": "u64"
        }
      ]
    },
    {
      "name": "solveIntentOnChain2",
      "discriminator": [
        69,
        161,
        170,
        214,
        156,
        30,
        3,
        24
      ],
      "accounts": [
        {
          "name": "solver",
          "writable": true,
          "signer": true
        },
        {
          "name": "config",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  99,
                  111,
                  110,
                  102,
                  105,
                  103
                ]
              }
            ]
          }
        },
        {
          "name": "tokenBMint"
        },
        {
          "name": "solverTokenBAta",
          "writable": true
        },
        {
          "name": "solvedRecord",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  115,
                  111,
                  108,
                  118,
                  101,
                  100
                ]
              },
              {
                "kind": "arg",
                "path": "srcChainId"
              },
              {
                "kind": "arg",
                "path": "srcIntentId"
              }
            ]
          }
        },
        {
          "name": "tokenProgram",
          "address": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
        },
        {
          "name": "associatedTokenProgram",
          "address": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
        },
        {
          "name": "systemProgram",
          "address": "11111111111111111111111111111111"
        }
      ],
      "args": [
        {
          "name": "srcChainId",
          "type": "u64"
        },
        {
          "name": "srcIntentId",
          "type": "u64"
        },
        {
          "name": "user",
          "type": {
            "array": [
              "u8",
              32
            ]
          }
        },
        {
          "name": "expectedAmountB",
          "type": "u64"
        },
        {
          "name": "amounts",
          "type": {
            "vec": "u64"
          }
        }
      ]
    },
    {
      "name": "sweepStealth",
      "discriminator": [
        47,
        178,
        10,
        61,
        47,
        22,
        161,
        120
      ],
      "accounts": [
        {
          "name": "user",
          "writable": true,
          "signer": true
        },
        {
          "name": "stealthRegistration",
          "pda": {
            "seeds": [
              {
                "kind": "const",
                "value": [
                  115,
                  116,
                  101,
                  97,
                  108,
                  116,
                  104
                ]
              },
              {
                "kind": "account",
                "path": "user"
              },
              {
                "kind": "arg",
                "path": "ephemeralSalt"
              }
            ]
          }
        },
        {
          "name": "mint"
        },
        {
          "name": "stealthAta",
          "writable": true,
          "pda": {
            "seeds": [
              {
                "kind": "account",
                "path": "stealthRegistration"
              },
              {
                "kind": "const",
                "value": [
                  6,
                  221,
                  246,
                  225,
                  215,
                  101,
                  161,
                  147,
                  217,
                  203,
                  225,
                  70,
                  206,
                  235,
                  121,
                  172,
                  28,
                  180,
                  133,
                  237,
                  95,
                  91,
                  55,
                  145,
                  58,
                  140,
                  245,
                  133,
                  126,
                  255,
                  0,
                  169
                ]
              },
              {
                "kind": "account",
                "path": "mint"
              }
            ],
            "program": {
              "kind": "const",
              "value": [
                140,
                151,
                37,
                143,
                78,
                36,
                137,
                241,
                187,
                61,
                16,
                41,
                20,
                142,
                13,
                131,
                11,
                90,
                19,
                153,
                218,
                255,
                16,
                132,
                4,
                142,
                123,
                216,
                219,
                233,
                248,
                89
              ]
            }
          }
        },
        {
          "name": "userAta",
          "writable": true
        },
        {
          "name": "tokenProgram",
          "address": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
        },
        {
          "name": "associatedTokenProgram",
          "address": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
        }
      ],
      "args": [
        {
          "name": "ephemeralSalt",
          "type": {
            "array": [
              "u8",
              32
            ]
          }
        }
      ]
    }
  ],
  "accounts": [
    {
      "name": "config",
      "discriminator": [
        155,
        12,
        170,
        224,
        30,
        250,
        204,
        130
      ]
    },
    {
      "name": "escrowAuthority",
      "discriminator": [
        28,
        249,
        37,
        100,
        161,
        37,
        215,
        224
      ]
    },
    {
      "name": "intentBids",
      "discriminator": [
        127,
        75,
        12,
        223,
        25,
        84,
        164,
        237
      ]
    },
    {
      "name": "intentRecord",
      "discriminator": [
        176,
        14,
        151,
        250,
        200,
        218,
        41,
        101
      ]
    },
    {
      "name": "relayerAuth",
      "discriminator": [
        150,
        94,
        140,
        87,
        225,
        46,
        141,
        5
      ]
    },
    {
      "name": "solvedRecord",
      "discriminator": [
        230,
        240,
        3,
        246,
        13,
        71,
        115,
        83
      ]
    },
    {
      "name": "stealthRegistration",
      "discriminator": [
        86,
        98,
        243,
        211,
        175,
        53,
        145,
        215
      ]
    }
  ],
  "events": [
    {
      "name": "bidPlaced",
      "discriminator": [
        135,
        53,
        176,
        83,
        193,
        69,
        108,
        61
      ]
    },
    {
      "name": "intentCancelled",
      "discriminator": [
        39,
        174,
        74,
        165,
        39,
        101,
        119,
        29
      ]
    },
    {
      "name": "intentCompleted",
      "discriminator": [
        203,
        107,
        96,
        43,
        219,
        247,
        41,
        16
      ]
    },
    {
      "name": "intentCreated",
      "discriminator": [
        184,
        46,
        156,
        205,
        169,
        254,
        11,
        108
      ]
    },
    {
      "name": "intentSettled",
      "discriminator": [
        112,
        202,
        229,
        173,
        39,
        129,
        254,
        38
      ]
    },
    {
      "name": "intentSolvedOnChain2",
      "discriminator": [
        239,
        207,
        177,
        7,
        218,
        134,
        219,
        123
      ]
    },
    {
      "name": "intentWon",
      "discriminator": [
        93,
        118,
        179,
        7,
        126,
        133,
        50,
        216
      ]
    },
    {
      "name": "solverDeposited",
      "discriminator": [
        105,
        70,
        214,
        17,
        103,
        201,
        47,
        41
      ]
    },
    {
      "name": "stealthRegistered",
      "discriminator": [
        107,
        183,
        234,
        73,
        237,
        244,
        241,
        155
      ]
    },
    {
      "name": "stealthSwept",
      "discriminator": [
        240,
        206,
        72,
        75,
        17,
        177,
        92,
        80
      ]
    }
  ],
  "errors": [
    {
      "code": 6000,
      "name": "invalidAuctionDuration",
      "msg": "Auction duration outside allowed range"
    },
    {
      "code": 6001,
      "name": "invalidAmount",
      "msg": "Invalid amount"
    },
    {
      "code": 6002,
      "name": "invalidReward",
      "msg": "Invalid reward"
    },
    {
      "code": 6003,
      "name": "invalidDestChainId",
      "msg": "Invalid destination chain ID"
    },
    {
      "code": 6004,
      "name": "invalidRecipient",
      "msg": "Invalid recipient"
    },
    {
      "code": 6005,
      "name": "auctionEnded",
      "msg": "Auction ended"
    },
    {
      "code": 6006,
      "name": "auctionStillActive",
      "msg": "Auction still active"
    },
    {
      "code": 6007,
      "name": "auctionAlreadyFinalized",
      "msg": "Auction already finalized"
    },
    {
      "code": 6008,
      "name": "intentAlreadyCompleted",
      "msg": "Intent already completed"
    },
    {
      "code": 6009,
      "name": "notDeposited",
      "msg": "Intent not deposited"
    },
    {
      "code": 6010,
      "name": "alreadyDeposited",
      "msg": "Already deposited"
    },
    {
      "code": 6011,
      "name": "bidNotHigher",
      "msg": "Bid not higher than previous"
    },
    {
      "code": 6012,
      "name": "bidderCapReached",
      "msg": "Bidder cap reached"
    },
    {
      "code": 6013,
      "name": "noBids",
      "msg": "No bids placed"
    },
    {
      "code": 6014,
      "name": "notWinningSolver",
      "msg": "Not the winning solver"
    },
    {
      "code": 6015,
      "name": "notAuthorized",
      "msg": "Not authorized"
    },
    {
      "code": 6016,
      "name": "tokenMintNotAllowed",
      "msg": "Token mint not in allowlist"
    },
    {
      "code": 6017,
      "name": "arrayLengthMismatch",
      "msg": "Recipient/amount array length mismatch"
    },
    {
      "code": 6018,
      "name": "emptyRecipients",
      "msg": "Empty recipients"
    },
    {
      "code": 6019,
      "name": "totalExceedsExpected",
      "msg": "Total exceeds expected amount"
    },
    {
      "code": 6020,
      "name": "totalExceedsDeposited",
      "msg": "Total exceeds deposited amount"
    },
    {
      "code": 6021,
      "name": "intentAlreadySolved",
      "msg": "Intent already solved on chain2"
    },
    {
      "code": 6022,
      "name": "tooEarlyToCancel",
      "msg": "Too early to cancel"
    },
    {
      "code": 6023,
      "name": "allowlistIndexOutOfBounds",
      "msg": "Allowlist index out of bounds"
    },
    {
      "code": 6024,
      "name": "accountMintMismatch",
      "msg": "Account mint mismatch"
    },
    {
      "code": 6025,
      "name": "accountOwnerMismatch",
      "msg": "Account owner mismatch"
    },
    {
      "code": 6026,
      "name": "recipientCountMismatch",
      "msg": "Recipient ATAs count mismatch"
    },
    {
      "code": 6027,
      "name": "stealthRegistrationMismatch",
      "msg": "Stealth registration mismatch"
    },
    {
      "code": 6028,
      "name": "bidZero",
      "msg": "Bid must be greater than zero"
    },
    {
      "code": 6029,
      "name": "overflow",
      "msg": "Arithmetic overflow"
    }
  ],
  "types": [
    {
      "name": "bid",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "solver",
            "type": "pubkey"
          },
          {
            "name": "bidAmount",
            "type": "u64"
          },
          {
            "name": "timestamp",
            "type": "i64"
          }
        ]
      }
    },
    {
      "name": "bidPlaced",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "solver",
            "type": "pubkey"
          },
          {
            "name": "bidAmount",
            "type": "u64"
          },
          {
            "name": "timestamp",
            "type": "i64"
          }
        ]
      }
    },
    {
      "name": "config",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "owner",
            "type": "pubkey"
          },
          {
            "name": "chainId",
            "type": "u64"
          },
          {
            "name": "allowedTokenBMints",
            "type": {
              "array": [
                "pubkey",
                2
              ]
            }
          },
          {
            "name": "nextIntentId",
            "type": "u64"
          },
          {
            "name": "escrowAuthorityBump",
            "type": "u8"
          },
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "escrowAuthority",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "intentBids",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "bids",
            "type": {
              "vec": {
                "defined": {
                  "name": "bid"
                }
              }
            }
          },
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "intentCancelled",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "user",
            "type": "pubkey"
          }
        ]
      }
    },
    {
      "name": "intentCompleted",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "solver",
            "type": "pubkey"
          }
        ]
      }
    },
    {
      "name": "intentCreated",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "user",
            "type": "pubkey"
          },
          {
            "name": "tokenAMint",
            "type": "pubkey"
          },
          {
            "name": "tokenBMint",
            "type": "pubkey"
          },
          {
            "name": "amountA",
            "type": "u64"
          },
          {
            "name": "expectedAmountB",
            "type": "u64"
          },
          {
            "name": "reward",
            "type": "u64"
          },
          {
            "name": "destChainId",
            "type": "u64"
          },
          {
            "name": "recipient",
            "type": {
              "array": [
                "u8",
                32
              ]
            }
          },
          {
            "name": "endTime",
            "type": "i64"
          }
        ]
      }
    },
    {
      "name": "intentRecord",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "user",
            "type": "pubkey"
          },
          {
            "name": "tokenAMint",
            "type": "pubkey"
          },
          {
            "name": "tokenBMint",
            "type": "pubkey"
          },
          {
            "name": "amountA",
            "type": "u64"
          },
          {
            "name": "expectedAmountB",
            "type": "u64"
          },
          {
            "name": "reward",
            "type": "u64"
          },
          {
            "name": "destChainId",
            "type": "u64"
          },
          {
            "name": "recipient",
            "type": {
              "array": [
                "u8",
                32
              ]
            }
          },
          {
            "name": "endTime",
            "type": "i64"
          },
          {
            "name": "winningSolver",
            "type": "pubkey"
          },
          {
            "name": "winningBid",
            "type": "u64"
          },
          {
            "name": "completed",
            "type": "bool"
          },
          {
            "name": "deposited",
            "type": "bool"
          },
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "intentSettled",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "solver",
            "type": "pubkey"
          },
          {
            "name": "amountA",
            "type": "u64"
          },
          {
            "name": "reward",
            "type": "u64"
          },
          {
            "name": "winningBid",
            "type": "u64"
          },
          {
            "name": "chain2IntentId",
            "type": "u64"
          }
        ]
      }
    },
    {
      "name": "intentSolvedOnChain2",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "srcChainId",
            "type": "u64"
          },
          {
            "name": "srcIntentId",
            "type": "u64"
          },
          {
            "name": "solver",
            "type": "pubkey"
          },
          {
            "name": "tokenBMint",
            "type": "pubkey"
          },
          {
            "name": "totalAmount",
            "type": "u64"
          }
        ]
      }
    },
    {
      "name": "intentWon",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "winningSolver",
            "type": "pubkey"
          },
          {
            "name": "winningBid",
            "type": "u64"
          }
        ]
      }
    },
    {
      "name": "relayerAuth",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "relayer",
            "type": "pubkey"
          },
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "solvedRecord",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "srcChainId",
            "type": "u64"
          },
          {
            "name": "srcIntentId",
            "type": "u64"
          },
          {
            "name": "solver",
            "type": "pubkey"
          },
          {
            "name": "tokenBMint",
            "type": "pubkey"
          },
          {
            "name": "totalAmount",
            "type": "u64"
          },
          {
            "name": "solvedAt",
            "type": "i64"
          },
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "solverDeposited",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "intentId",
            "type": "u64"
          },
          {
            "name": "solver",
            "type": "pubkey"
          },
          {
            "name": "amount",
            "type": "u64"
          }
        ]
      }
    },
    {
      "name": "stealthRegistered",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "user",
            "type": "pubkey"
          },
          {
            "name": "stealthPda",
            "type": "pubkey"
          },
          {
            "name": "ephemeralSalt",
            "type": {
              "array": [
                "u8",
                32
              ]
            }
          }
        ]
      }
    },
    {
      "name": "stealthRegistration",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "user",
            "type": "pubkey"
          },
          {
            "name": "ephemeralSalt",
            "type": {
              "array": [
                "u8",
                32
              ]
            }
          },
          {
            "name": "createdAt",
            "type": "i64"
          },
          {
            "name": "bump",
            "type": "u8"
          }
        ]
      }
    },
    {
      "name": "stealthSwept",
      "type": {
        "kind": "struct",
        "fields": [
          {
            "name": "user",
            "type": "pubkey"
          },
          {
            "name": "stealthPda",
            "type": "pubkey"
          },
          {
            "name": "mint",
            "type": "pubkey"
          },
          {
            "name": "amount",
            "type": "u64"
          }
        ]
      }
    }
  ]
};
