import { registerHooks } from 'node:module'

// 源码沿用 Vite 的无扩展名导入；测试直接使用 Node 的 TypeScript 支持，无额外依赖。
registerHooks({
  resolve(specifier, context, nextResolve) {
    try {
      return nextResolve(specifier, context)
    } catch (error) {
      if (error.code === 'ERR_MODULE_NOT_FOUND' && specifier.startsWith('.') && context.parentURL?.includes('/src/')) {
        return nextResolve(`${specifier}.ts`, context)
      }
      throw error
    }
  },
})
