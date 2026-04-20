# @runwisp/typescript-config

Shared TypeScript configuration for the RunWisp monorepo.

## Configs

| Config | Path        | Description                                                                       |
| ------ | ----------- | --------------------------------------------------------------------------------- |
| `base` | `base.json` | Strict base config — ESNext target, bundler resolution, all strict checks enabled |

## Usage

Extend from your project's `tsconfig.json`:

```jsonc
{
    "extends": "@runwisp/typescript-config/base.json",
    "compilerOptions": {
        // project-specific overrides
    },
}
```

## License

[Apache-2.0](LICENSE)
