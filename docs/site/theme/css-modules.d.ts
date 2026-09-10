/**
 * Rspress resolves `*.module.css` imports to a class-name map at build
 * time; TypeScript needs telling that separately, or every styles import
 * is a missing module.
 */
declare module '*.module.css' {
  const classes: Record<string, string>;
  export default classes;
}
