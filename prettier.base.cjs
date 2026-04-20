const fs = require("node:fs");
const path = require("node:path");

const candidateTailwindStylesheets = ["src/routes/layout.css", "src/app.css", "src/styles.css"];

const resolveFromCwd = (moduleId) => {
	try {
		return require.resolve(moduleId, { paths: [process.cwd()] });
	} catch {
		return null;
	}
};

const tailwindPackageJsonPath = resolveFromCwd("tailwindcss/package.json");
const tailwindVersion = tailwindPackageJsonPath
	? JSON.parse(fs.readFileSync(tailwindPackageJsonPath, "utf8")).version
	: null;
const tailwindMajor = tailwindVersion ? Number.parseInt(tailwindVersion.split(".")[0], 10) : null;
const useTailwindPlugin = Number.isFinite(tailwindMajor) && tailwindMajor >= 4;

const tailwindStylesheet = candidateTailwindStylesheets
	.map((candidate) => path.join(process.cwd(), candidate))
	.find((absolutePath) => fs.existsSync(absolutePath));

module.exports = {
	tabWidth: 4,
	printWidth: 100,
	plugins: [
		"prettier-plugin-svelte",
		...(useTailwindPlugin ? ["prettier-plugin-tailwindcss"] : [])
	],
	overrides: [
		{
			files: "*.svelte",
			options: {
				parser: "svelte"
			}
		}
	],
	...(useTailwindPlugin && tailwindStylesheet
		? {
				tailwindStylesheet: `./${path
					.relative(process.cwd(), tailwindStylesheet)
					.split(path.sep)
					.join("/")}`
			}
		: {})
};
