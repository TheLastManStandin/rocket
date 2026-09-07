/**
 * The Telegram Stars mark, traced off the reference board so a star here reads
 * as the same currency it does there: a gold body with a lit facet and a white
 * highlight along the top-left edge, rather than a flat five-pointed outline.
 *
 * The artwork is declared once by StarDefs and stamped out by <use>, so a table
 * of twenty riders carries one copy of the paths and gradients between them.
 */
export function StarDefs() {
  return (
    <svg className="star-defs" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient
          id="star-body"
          x1="11.9629"
          y1="3.52907"
          x2="12.0816"
          y2="20.8273"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#FFB600" />
          <stop offset="0.5" stopColor="#F69C00" />
          <stop offset="1" stopColor="#ED8200" />
        </linearGradient>
        <linearGradient
          id="star-edge"
          x1="11.9607"
          y1="3.98277"
          x2="12.0886"
          y2="20.637"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#CB7000" />
          <stop offset="0.5" stopColor="#C76600" />
          <stop offset="1" stopColor="#C45C00" />
        </linearGradient>
        <linearGradient
          id="star-face"
          x1="16.3776"
          y1="21.2757"
          x2="9.79119"
          y2="3.87536"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#FFA200" />
          <stop offset="0.5" stopColor="#FFC12F" />
          <stop offset="1" stopColor="#FFDF5E" />
        </linearGradient>

        <g id="tg-star">
          <path d={BODY} fill="url(#star-body)" />
          <path
            d={BODY}
            fill="none"
            stroke="url(#star-edge)"
            strokeWidth="0.390625"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          <path d={FACE} fill="url(#star-face)" />
          <path d={LIT} fill="#FFD967" />
          <path
            d={HIGHLIGHT}
            fill="none"
            stroke="white"
            strokeWidth="0.830078"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </g>
      </defs>
    </svg>
  );
}

interface Props {
  /** Extra classes on top of `star`, which carries the size. */
  className?: string;
}

export function Star({ className }: Props) {
  return (
    <svg
      viewBox="0 0 24 24"
      className={className ? `star ${className}` : "star"}
      aria-hidden="true"
      focusable="false"
    >
      <use href="#tg-star" />
    </svg>
  );
}

const BODY =
  "M8.92784 8.30934L11.0513 4.09059C11.295 3.61715 11.9513 3.40621 12.4435 3.64059C12.6357 3.73434 12.8325 3.91246 12.9263 4.10934L14.9466 8.22965C15.1107 8.56715 15.3778 8.7359 15.7435 8.78277L19.9013 9.27965C20.5153 9.35465 20.9513 9.96871 20.881 10.55C20.8528 10.789 20.745 11.0421 20.5763 11.2109L17.2482 14.5062C17.1122 14.6421 17.0794 14.7968 17.1028 14.9843L17.6419 19.4421C17.7216 20.0843 17.2107 20.675 16.5778 20.7546C16.3388 20.7828 16.0622 20.7546 15.8466 20.6375L12.3919 18.7343C12.0638 18.5656 11.8341 18.575 11.5763 18.7109L7.99503 20.5671C7.4794 20.8343 6.76221 20.6234 6.49971 20.1031C6.40128 19.9062 6.32159 19.7 6.35909 19.4375L6.63565 17.4171C6.78565 16.325 7.4419 15.4109 8.33253 14.9515L12.1247 12.9781C12.3638 12.8375 12.3778 12.7062 12.0263 12.7531L7.3294 13.3953C6.57003 13.5031 5.7544 13.2312 5.15909 12.7437L3.52784 11.389C3.07315 11.0328 2.99346 10.2406 3.37784 9.76246C3.55596 9.54215 3.8419 9.34059 4.11846 9.30309L8.35596 8.75465C8.62784 8.72652 8.80596 8.55777 8.92784 8.30934Z";

const FACE =
  "M16.8568 19.2974L16.3083 14.9474C16.2614 14.5724 16.3927 14.2302 16.6927 13.9443L19.913 10.7521C19.9833 10.6818 20.0208 10.5365 20.0208 10.438C20.0208 10.199 19.8614 10.063 19.6318 10.0349L15.563 9.53804C14.9958 9.46773 14.4989 9.10211 14.2458 8.58648L12.2958 4.59273C12.2583 4.51773 12.1646 4.45679 12.0896 4.42398C11.9911 4.37711 11.7802 4.42867 11.6911 4.61617L9.55831 8.71773C9.34738 9.13961 8.93956 9.43492 8.47081 9.49586L4.35987 10.0584C4.238 10.0724 4.12081 10.1193 4.04581 10.2177C3.91925 10.3818 3.97081 10.7615 4.17237 10.9302L5.738 12.2146C6.188 12.5849 6.73175 12.7677 7.30831 12.688L12.1786 11.9849C12.5302 11.9334 12.8724 12.1162 13.0318 12.4302C13.2333 12.8334 13.0552 13.3162 12.6521 13.5224L8.72394 15.538C8.02081 15.899 7.538 16.5834 7.4255 17.3755L7.14894 19.3724C7.13488 19.4615 7.17706 19.5974 7.238 19.663C7.39269 19.8365 7.56613 19.888 7.77238 19.7849L11.2833 17.9755C11.7708 17.7271 12.2724 17.7693 12.6989 18.0037L16.088 19.8646C16.1864 19.9162 16.3411 19.9349 16.4489 19.9115C16.6833 19.8552 16.8943 19.5974 16.8568 19.2974Z";

const LIT =
  "M15.5607 9.53774C14.9935 9.46743 14.4966 9.1018 14.2435 8.58618L12.2935 4.59243C12.256 4.51743 12.1904 4.46118 12.12 4.42837C11.9935 4.37212 11.7451 4.40962 11.656 4.59243L9.55599 8.71743C9.34505 9.1393 8.93724 9.43462 8.46849 9.49555L4.35755 10.0581C4.23568 10.0721 4.11849 10.119 4.04349 10.2174C3.91224 10.3956 3.96849 10.7612 4.17005 10.9299L5.73568 12.2143C6.18568 12.5846 6.72942 12.7674 7.30599 12.6877L12.1763 11.9846C12.5279 11.9331 12.87 12.1159 13.0294 12.4299C13.0482 12.4674 13.0622 12.5049 13.0763 12.5424C14.3419 11.8065 16.081 10.7706 17.5388 9.78149L15.5607 9.53774Z";

const HIGHLIGHT =
  "M4.38867 10.4518L8.66836 9.84707C9.13242 9.78145 9.62461 9.41113 9.87305 9.00801L12.0012 4.86426";
