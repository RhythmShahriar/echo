export async function register() {
  const { register } = await import('@rhythmsh/echo/next');
  await register();
}
