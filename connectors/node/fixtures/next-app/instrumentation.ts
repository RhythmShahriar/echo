export async function register() {
  const { register } = await import('@rhythm/echo/next');
  await register();
}
