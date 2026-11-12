import { defineConfig, type UserConfig } from 'vite';
import { resolve } from 'node:path';

/**
 * 多入口构建：
 *  - 默认（UI）：options.html + popup.html + public/manifest.json → dist/
 *  - BUILD_TARGET=background：service worker（ESM，manifest type: module）
 *  - BUILD_TARGET=content   ：content script（IIFE 单文件，content scripts 不支持模块）
 *  - BUILD_TARGET=inject    ：MAIN_WORLD 注入脚本（IIFE 单文件，需在页面 hook WS/XHR）
 *
 * `npm run build` 会串联执行全部四次构建，产物均在 extension/dist/。
 */

type Target = 'ui' | 'background' | 'content' | 'inject';

function detectTarget(mode: string): Target {
  if (mode === 'background' || mode === 'content' || mode === 'inject') {
    return mode;
  }
  return 'ui';
}

const target = detectTarget(process.env.BUILD_TARGET ?? process.env.npm_lifecycle_event ?? 'build');

const scriptConfig = (entry: string, outName: string, format: 'es' | 'iife'): UserConfig => ({
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    target: 'es2022',
    minify: false,
    lib: {
      entry: resolve(__dirname, entry),
      formats: [format],
      name: 'jobChatCopilot',
      fileName: () => `${outName}.js`,
    },
    rollupOptions: {
      output: {
        // 单文件产物，禁止代码分割（content script / 注入脚本不能有 import）
        inlineDynamicImports: true,
        entryFileNames: `${outName}.js`,
        assetFileNames: `${outName}.[ext]`,
        chunkFileNames: `${outName}-[name].js`,
      },
    },
  },
});

export default defineConfig(() => {
  switch (target) {
    case 'background':
      return scriptConfig('src/background/index.ts', 'background', 'es');
    case 'content':
      return scriptConfig('src/content/index.ts', 'content', 'iife');
    case 'inject':
      return scriptConfig('src/content/inject.ts', 'inject', 'iife');
    default:
      return {
        publicDir: 'public',
        build: {
          outDir: 'dist',
          emptyOutDir: true,
          target: 'es2022',
          rollupOptions: {
            input: {
              options: resolve(__dirname, 'options.html'),
              popup: resolve(__dirname, 'popup.html'),
            },
          },
        },
      } satisfies UserConfig;
  }
});
