using App.Core;
using UnityEngine;

namespace App.Systems
{
    public sealed class AudioSystem : AppSystemBase
    {
        private AudioSource _bgmSource;
        private AudioSource _sfxSource;

        protected override void OnInitialize()
        {
            GameObject audioRoot = new GameObject("AudioRoot");
            audioRoot.transform.SetParent(Root.transform);

            _bgmSource = audioRoot.AddComponent<AudioSource>();
            _bgmSource.loop = true;
            _bgmSource.playOnAwake = false;

            _sfxSource = audioRoot.AddComponent<AudioSource>();
            _sfxSource.loop = false;
            _sfxSource.playOnAwake = false;
        }

        public void PlayBgm(string bgmName)
        {
            if (string.IsNullOrWhiteSpace(bgmName))
            {
                return;
            }

            string path = AppConfig.BgmPath + bgmName;
            AudioClip clip = Root.Asset.Load<AudioClip>(path);

            if (clip == null)
            {
                return;
            }

            _bgmSource.clip = clip;
            _bgmSource.Play();

            Root.Log.Info<AudioSystem>($"Play BGM: {bgmName}");
        }

        public void StopBgm()
        {
            if (_bgmSource == null)
            {
                return;
            }

            _bgmSource.Stop();
            _bgmSource.clip = null;
        }

        public void PlaySfx(string sfxName)
        {
            if (string.IsNullOrWhiteSpace(sfxName))
            {
                return;
            }

            string path = AppConfig.SfxPath + sfxName;
            AudioClip clip = Root.Asset.Load<AudioClip>(path);

            if (clip == null)
            {
                return;
            }

            _sfxSource.PlayOneShot(clip);
        }

        public void SetBgmVolume(float volume)
        {
            if (_bgmSource != null)
            {
                _bgmSource.volume = Mathf.Clamp01(volume);
            }
        }

        public void SetSfxVolume(float volume)
        {
            if (_sfxSource != null)
            {
                _sfxSource.volume = Mathf.Clamp01(volume);
            }
        }
    }
}