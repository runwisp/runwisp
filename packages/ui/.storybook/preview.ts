import type { Preview } from "@storybook/sveltekit";
import "../src/lib/theme.css";

const preview: Preview = {
    parameters: {
        controls: {
            matchers: {
                color: /(background|color)$/i,
                date: /Date$/i,
            },
        },
        backgrounds: {
            default: "light",
            values: [
                { name: "light", value: "oklch(0.98 0.005 280)" },
                { name: "white", value: "#ffffff" },
                { name: "dark", value: "oklch(0.12 0.012 280)" },
            ],
        },
    },
};

export default preview;
