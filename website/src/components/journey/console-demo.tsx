export function ConsoleDemo() {
  return (
    <figure className="console-demo">
      <video controls playsInline preload="none" poster="/demo/console/poster.png" width={1280} height={800} aria-label="Starport Console tour">
        <source src="/demo/console/console.mp4" type="video/mp4" />
        <source src="/demo/console/console.webm" type="video/webm" />
        <a href="/demo/console/console.mp4">Watch the Console tour</a>
      </video>
      <figcaption>
        Choose a model, inspect its offerings, and open provider credential setup.{' '}
        <a href="/demo/console/TRANSCRIPT.md">Read the transcript</a> or{' '}
        <a href="/demo/console/console.mp4">open the video</a>.
      </figcaption>
    </figure>
  );
}
